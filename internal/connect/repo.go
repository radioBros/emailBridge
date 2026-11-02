package connect

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Repo is Mongo access for connectLinks (hosted mode) and account identity.
type Repo struct {
	DB *store.DB
}

func (r *Repo) links() *mongo.Collection { return r.DB.Collection(CollectionLinks) }
func (r *Repo) accounts() *mongo.Collection {
	return r.DB.Collection("accounts")
}

func (r *Repo) insertLink(ctx context.Context, l *Link) error {
	_, err := r.links().InsertOne(ctx, l)
	return err
}

func (r *Repo) getLink(ctx context.Context, id string) (*Link, error) {
	var l Link
	err := r.links().FindOne(ctx, bson.M{"_id": id}).Decode(&l)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.link")
	}
	return &l, nil
}

func (r *Repo) markLinkUsed(ctx context.Context, id string, at time.Time) error {
	res, err := r.links().UpdateOne(ctx,
		bson.M{"_id": id, "usedAt": bson.M{"$exists": false}},
		bson.M{"$set": bson.M{"usedAt": at}},
	)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "connect.link.use")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeConnectLinkInvalid, "The connect link is invalid or expired.")
	}
	return nil
}

type accountDoc struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	Email          string    `bson:"email"`
	Identity       *Identity `bson:"identity,omitempty"`
	Version        int64     `bson:"version"`
}

func (r *Repo) getAccount(ctx context.Context, org, id string) (*accountDoc, error) {
	var a accountDoc
	err := r.accounts().FindOne(ctx, bson.M{"_id": id, "organizationId": org, "deletedAt": bson.M{"$exists": false}}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.account")
	}
	return &a, nil
}

func (r *Repo) setIdentity(ctx context.Context, org, id string, pin Identity, version int64) error {
	res, err := r.accounts().UpdateOne(ctx,
		bson.M{"_id": id, "organizationId": org, "version": version},
		bson.M{"$set": bson.M{"identity": pin, "updatedAt": pin.PinnedAt}, "$inc": bson.M{"version": 1}},
	)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "connect.repin")
	}
	if res.MatchedCount == 0 {
		return errs.New(errs.CodeConflict, "The account was modified; retry.")
	}
	return nil
}

type orgDoc struct {
	ID       string         `bson:"_id"`
	Name     string         `bson:"name"`
	Branding map[string]any `bson:"branding,omitempty"`
	Version  int64          `bson:"version"`
}

func (r *Repo) getOrg(ctx context.Context, id string) (*orgDoc, error) {
	var o orgDoc
	err := r.DB.Collection("organizations").FindOne(ctx, bson.M{"_id": id}).Decode(&o)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The organisation does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.org")
	}
	return &o, nil
}

func (r *Repo) setOrgBranding(ctx context.Context, id string, branding map[string]any, version int64) (*orgDoc, error) {
	res, err := r.DB.Collection("organizations").UpdateOne(ctx,
		bson.M{"_id": id, "version": version},
		bson.M{
			"$set": bson.M{"branding": branding, "updatedAt": time.Now().UTC()},
			"$inc": bson.M{"version": 1},
		},
	)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.branding")
	}
	if res.MatchedCount == 0 {
		return nil, errs.New(errs.CodeConflict, "The organisation was modified; retry.")
	}
	return r.getOrg(ctx, id)
}

type oauthAppDoc struct {
	ID       string `bson:"_id"`
	Name     string `bson:"name"`
	Provider string `bson:"provider"`
	Enabled  bool   `bson:"enabled"`
}

func (r *Repo) listOAuthApps(ctx context.Context, org string) ([]OAuthAppBrief, error) {
	cur, err := r.DB.Collection("oauthApps").Find(ctx,
		bson.M{"organizationId": org, "enabled": true},
		options.Find().SetProjection(bson.M{"name": 1, "provider": 1}).SetLimit(50),
	)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.oauthApps")
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []OAuthAppBrief
	for cur.Next(ctx) {
		var a oauthAppDoc
		if err := cur.Decode(&a); err != nil {
			return nil, err
		}
		out = append(out, OAuthAppBrief{ID: a.ID, Name: a.Name, Provider: a.Provider})
	}
	return out, cur.Err()
}

// EnsureIndexes is a no-op beyond documenting hosted fields (indexes from P6.1).
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, CollectionLinks,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "mode", Value: 1}}},
	)
}

// EnsureAssetIndexes creates brandingAssets indexes (W68).
func EnsureAssetIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, CollectionAssets,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}, {Key: "kind", Value: 1}},
			Options: options.Index().SetName("branding_org_kind"),
		},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}},
			Options: options.Index().SetName("branding_org"),
		},
	)
}

func (r *Repo) assets() *mongo.Collection { return r.DB.Collection(CollectionAssets) }

// InsertAsset implements AssetStore.
func (r *Repo) InsertAsset(ctx context.Context, a *BrandingAsset) error {
	_, err := r.assets().InsertOne(ctx, a)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "connect.asset.insert")
	}
	return nil
}

// GetAsset implements AssetStore.
func (r *Repo) GetAsset(ctx context.Context, id string) (*BrandingAsset, error) {
	var a BrandingAsset
	err := r.assets().FindOne(ctx, bson.M{"_id": id}).Decode(&a)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.asset.get")
	}
	return &a, nil
}

// ListAssets implements AssetStore.
func (r *Repo) ListAssets(ctx context.Context, org string, kind AssetKind) ([]BrandingAsset, error) {
	filter := bson.M{"organizationId": org}
	if kind != "" {
		filter["kind"] = kind
	}
	cur, err := r.assets().Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: -1}}).SetLimit(100))
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "connect.asset.list")
	}
	defer func() { _ = cur.Close(ctx) }()
	var out []BrandingAsset
	for cur.Next(ctx) {
		var a BrandingAsset
		if err := cur.Decode(&a); err != nil {
			return nil, errs.Wrap(err, errs.CodeInternalError, "connect.asset.list")
		}
		out = append(out, a)
	}
	if out == nil {
		out = []BrandingAsset{}
	}
	return out, cur.Err()
}

// DeleteAsset implements AssetStore.
func (r *Repo) DeleteAsset(ctx context.Context, org, id string) error {
	res, err := r.assets().DeleteOne(ctx, bson.M{"_id": id, "organizationId": org})
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "connect.asset.delete")
	}
	if res.DeletedCount == 0 {
		return errs.New(errs.CodeNotFound, "The branding asset does not exist.")
	}
	return nil
}

// AssetQuota implements AssetStore.
func (r *Repo) AssetQuota(ctx context.Context, org string) (int, int64, error) {
	cur, err := r.assets().Find(ctx, bson.M{"organizationId": org},
		options.Find().SetProjection(bson.M{"size": 1}))
	if err != nil {
		return 0, 0, errs.Wrap(err, errs.CodeInternalError, "connect.asset.quota")
	}
	defer func() { _ = cur.Close(ctx) }()
	n := 0
	var total int64
	for cur.Next(ctx) {
		var row struct {
			Size int64 `bson:"size"`
		}
		if err := cur.Decode(&row); err != nil {
			return 0, 0, err
		}
		n++
		total += row.Size
	}
	return n, total, cur.Err()
}

// Migration returns store migration number n (P6.4 hosted link index).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "connect hosted link mode index", Up: EnsureIndexes}
}

// MigrationAssets returns migration 44 (W68 brandingAssets indexes).
func MigrationAssets(number int) store.Migration {
	return store.Migration{Number: number, Name: "branding assets indexes", Up: EnsureAssetIndexes}
}
