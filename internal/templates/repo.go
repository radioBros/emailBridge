package templates

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo is MongoDB access for templates and versions.
type Repo struct {
	DB *store.DB
}

func (r *Repo) col() *mongo.Collection { return r.DB.Collection(Collection) }
func (r *Repo) versions() *mongo.Collection {
	return r.DB.Collection(VersionsCollection)
}

func (r *Repo) insert(ctx context.Context, t *Template, v *Version) error {
	return r.DB.WithTx(ctx, func(tx context.Context) error {
		if _, err := r.col().InsertOne(tx, t); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errs.New(errs.CodeTemplateNameTaken, "A template with this name already exists.").
					WithSafe("name", t.Name)
			}
			return errs.Wrap(err, errs.CodeInternalError, "templates.insert")
		}
		if _, err := r.versions().InsertOne(tx, v); err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "templates.version")
		}
		return nil
	})
}

func (r *Repo) get(ctx context.Context, id string) (*Template, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var t Template
	err = r.col().FindOne(ctx, filter).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeTemplateNotFound, "The template does not exist.").WithSafe("templateId", id)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "templates.get")
	}
	t.HTMLFormat = formatOf(t.HTMLFormat)
	return &t, nil
}

func (r *Repo) getVersion(ctx context.Context, templateID string, n int) (*Version, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": versionID(templateID, n)})
	if err != nil {
		return nil, err
	}
	var v Version
	err = r.versions().FindOne(ctx, filter).Decode(&v)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeTemplateNotFound, "The template version does not exist.").
			WithSafe("templateId", templateID).WithSafe("version", n)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "templates.version.get")
	}
	return &v, nil
}

func (r *Repo) replace(ctx context.Context, t *Template, oldVersion int64, ver *Version) error {
	return r.DB.WithTx(ctx, func(tx context.Context) error {
		filter, err := store.Scoped(tx, bson.M{"_id": t.ID, "version": oldVersion})
		if err != nil {
			return err
		}
		res, err := r.col().ReplaceOne(tx, filter, t)
		if err != nil {
			if mongo.IsDuplicateKeyError(err) {
				return errs.New(errs.CodeTemplateNameTaken, "A template with this name already exists.").
					WithSafe("name", t.Name)
			}
			return errs.Wrap(err, errs.CodeInternalError, "templates.replace")
		}
		if res.MatchedCount == 0 {
			return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
		}
		if ver != nil {
			if _, err := r.versions().InsertOne(tx, ver); err != nil {
				return errs.Wrap(err, errs.CodeInternalError, "templates.version")
			}
		}
		return nil
	})
}

func (r *Repo) delete(ctx context.Context, id string, version int64) error {
	return r.DB.WithTx(ctx, func(tx context.Context) error {
		filter, err := store.Scoped(tx, bson.M{"_id": id})
		if err != nil {
			return err
		}
		if version > 0 {
			filter["version"] = version
		}
		res, err := r.col().DeleteOne(tx, filter)
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "templates.delete")
		}
		if res.DeletedCount == 0 {
			if version > 0 {
				return errs.New(errs.CodeVersionMismatch, "The resource version does not match If-Match.")
			}
			return errs.New(errs.CodeTemplateNotFound, "The template does not exist.").WithSafe("templateId", id)
		}
		_, _ = r.versions().DeleteMany(tx, bson.M{"templateId": id})
		return nil
	})
}

func versionID(templateID string, n int) string {
	return fmt.Sprintf("%s:v%d", templateID, n)
}

// contentOf is what preview and send render: for MJML templates the HTML compiled at save time.
func contentOf(t *Template) content {
	return content{
		Subject: t.Subject, Text: t.Text, HTML: renderableHTML(t.HTMLFormat, t.HTML, t.HTMLCompiled),
		Partials: t.Partials, Strict: t.Strict, Locale: t.Locale, Timezone: t.Timezone,
	}
}

// sourcesOf is the authored sources (the MJML itself for MJML templates), for validation.
func sourcesOf(t *Template) content {
	return content{
		Subject: t.Subject, Text: t.Text, HTML: t.HTML, Partials: t.Partials,
		Strict: t.Strict, Locale: t.Locale, Timezone: t.Timezone,
	}
}

func contentOfVersion(v *Version) content {
	return content{
		Subject: v.Subject, Text: v.Text, HTML: renderableHTML(v.HTMLFormat, v.HTML, v.HTMLCompiled),
		Partials: v.Partials, Strict: v.Strict, Locale: v.Locale, Timezone: v.Timezone,
	}
}

func renderableHTML(format, html, compiled string) string {
	if formatOf(format) == HTMLFormatMJML {
		return compiled
	}
	return html
}

func snapshot(t *Template, actor string, at time.Time) *Version {
	return &Version{
		ID: versionID(t.ID, t.CurrentVersion), OrganizationID: t.OrganizationID, TemplateID: t.ID,
		Version: t.CurrentVersion, Subject: t.Subject, Text: t.Text, HTML: t.HTML,
		HTMLFormat: formatOf(t.HTMLFormat), HTMLCompiled: t.HTMLCompiled,
		Partials: cloneMap(t.Partials), Strict: t.Strict, Locale: t.Locale, Timezone: t.Timezone,
		CreatedBy: actor, CreatedAt: at,
	}
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// EnsureIndexes creates templates indexes (migration 27).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	if err := db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: -1}}},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "name", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
	); err != nil {
		return err
	}
	return db.EnsureIndexes(ctx, VersionsCollection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "templateId", Value: 1}, {Key: "version", Value: -1}}},
	)
}
