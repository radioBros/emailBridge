package deliverytest

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

// Repo is MongoDB access for deliveryTests.
type Repo struct {
	DB *store.DB
}

func (r *Repo) coll() *mongo.Collection { return r.DB.Collection(Collection) }

func (r *Repo) insert(ctx context.Context, t *Test) error {
	_, err := r.coll().InsertOne(ctx, t)
	return err
}

func (r *Repo) get(ctx context.Context, id string) (*Test, error) {
	filter, err := store.Scoped(ctx, bson.M{"_id": id})
	if err != nil {
		return nil, err
	}
	var t Test
	if err := r.coll().FindOne(ctx, filter).Decode(&t); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeNotFound, "The delivery test does not exist.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "deliverytest.get")
	}
	return &t, nil
}

// GetByID loads without tenancy (job runner).
func (r *Repo) GetByID(ctx context.Context, orgID, id string) (*Test, error) {
	var t Test
	err := r.coll().FindOne(ctx, bson.M{"_id": id, "organizationId": orgID}).Decode(&t)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeNotFound, "The delivery test does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "deliverytest.get")
	}
	return &t, nil
}

func (r *Repo) replace(ctx context.Context, t *Test) error {
	_, err := r.coll().ReplaceOne(ctx, bson.M{"_id": t.ID, "organizationId": t.OrganizationID}, t)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "deliverytest.replace")
	}
	return nil
}

// EnsureIndexes creates deliveryTests indexes.
func EnsureIndexes(ctx context.Context, db *store.DB) error {
	return db.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "_id", Value: 1}}},
		mongo.IndexModel{Keys: bson.D{{Key: "organizationId", Value: 1}, {Key: "accountId", Value: 1}, {Key: "createdAt", Value: -1}}},
	)
}

// FindByMessageID looks up an indexed message by Message-ID header.
func FindByMessageID(ctx context.Context, db *store.DB, accountID, messageIDHeader string) (*Hit, error) {
	var msg struct {
		ID           string    `bson:"_id"`
		MailboxID    string    `bson:"mailboxId"`
		ArchiveRef   string    `bson:"archiveRef,omitempty"`
		InternalDate time.Time `bson:"internalDate"`
	}
	err := db.Collection("messages").FindOne(ctx, bson.M{
		"accountId": accountID, "messageIdHeader": messageIDHeader,
	}, options.FindOne().SetSort(bson.D{{Key: "internalDate", Value: -1}})).Decode(&msg)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "deliverytest.index")
	}
	var mbx struct {
		Path       string `bson:"path"`
		SpecialUse string `bson:"specialUse,omitempty"`
	}
	if err := db.Collection("mailboxes").FindOne(ctx, bson.M{"_id": msg.MailboxID}).Decode(&mbx); err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.Wrap(err, errs.CodeInternalError, "deliverytest.mailbox")
		}
	}
	return &Hit{
		MessageID: msg.ID, MailboxPath: mbx.Path, SpecialUse: mbx.SpecialUse,
		ArchiveRef: msg.ArchiveRef, InternalDate: msg.InternalDate,
	}, nil
}

// Hit is one indexed probe arrival.
type Hit struct {
	MessageID    string
	MailboxPath  string
	SpecialUse   string
	ArchiveRef   string
	InternalDate time.Time
}
