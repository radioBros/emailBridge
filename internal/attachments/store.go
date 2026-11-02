package attachments

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

// docStore is the Mongo-backed attachment index (tests may swap a memory impl).
type docStore interface {
	get(ctx context.Context, accountID, messageID, attID string) (*Doc, error)
	getByID(ctx context.Context, orgID, attID string) (*Doc, error)
	findByPart(ctx context.Context, accountID, messageID, partID string) (*Doc, error)
	upsert(ctx context.Context, d *Doc) error
	listByMessage(ctx context.Context, accountID, messageID string) ([]*Doc, error)
	deleteMessage(ctx context.Context, accountID, messageID string) (int64, error)
	ensure(ctx context.Context) error
}

type mongoStore struct {
	DB *store.DB
}

var _ docStore = (*mongoStore)(nil)

func (s *mongoStore) coll() *mongo.Collection {
	return s.DB.Collection(Collection)
}

func (s *mongoStore) get(ctx context.Context, accountID, messageID, attID string) (*Doc, error) {
	if !store.IsID(store.PrefixAttachment, attID) {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.").WithSafe("attachmentId", attID)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": attID, "accountId": accountID, "messageId": messageID})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.scoped")
	}
	var d Doc
	err = s.coll().FindOne(ctx, filter).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.").WithSafe("attachmentId", attID)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.get")
	}
	return &d, nil
}

func (s *mongoStore) getByID(ctx context.Context, orgID, attID string) (*Doc, error) {
	if !store.IsID(store.PrefixAttachment, attID) {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
	}
	var d Doc
	err := s.coll().FindOne(ctx, bson.M{"_id": attID, "organizationId": orgID}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeAttachmentNotFound, "The attachment does not exist.")
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.getByID")
	}
	return &d, nil
}

func (s *mongoStore) findByPart(ctx context.Context, accountID, messageID, partID string) (*Doc, error) {
	filter, err := store.Scoped(ctx, bson.M{"accountId": accountID, "messageId": messageID, "partId": partID})
	if err != nil {
		return nil, err
	}
	var d Doc
	err = s.coll().FindOne(ctx, filter).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return &d, err
}

func (s *mongoStore) upsert(ctx context.Context, d *Doc) error {
	_, err := s.coll().ReplaceOne(ctx, bson.M{"_id": d.ID}, d, options.Replace().SetUpsert(true))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "attachments.upsert")
	}
	return nil
}

func (s *mongoStore) listByMessage(ctx context.Context, accountID, messageID string) ([]*Doc, error) {
	filter, err := store.Scoped(ctx, bson.M{"accountId": accountID, "messageId": messageID})
	if err != nil {
		return nil, err
	}
	cur, err := s.coll().Find(ctx, filter)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.list")
	}
	var out []*Doc
	if err := cur.All(ctx, &out); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "attachments.list")
	}
	return out, nil
}

func (s *mongoStore) deleteMessage(ctx context.Context, accountID, messageID string) (int64, error) {
	filter, err := store.Scoped(ctx, bson.M{"accountId": accountID, "messageId": messageID})
	if err != nil {
		return 0, err
	}
	res, err := s.coll().DeleteMany(ctx, filter)
	if err != nil {
		return 0, errs.Wrap(err, errs.CodeInternalError, "attachments.deleteMessage")
	}
	return res.DeletedCount, nil
}

func (s *mongoStore) ensure(ctx context.Context) error {
	return s.DB.EnsureIndexes(ctx, Collection,
		mongo.IndexModel{
			Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "messageId", Value: 1}, {Key: "partId", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("att_msg_part"),
		},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "accountId", Value: 1}, {Key: "messageId", Value: 1}},
			Options: options.Index().SetName("att_msg"),
		},
		mongo.IndexModel{
			Keys:    bson.D{{Key: "organizationId", Value: 1}},
			Options: options.Index().SetName("att_org"),
		},
	)
}

func nowUTC() time.Time { return time.Now().UTC() }
