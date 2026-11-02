package submit

import (
	"context"
	"errors"
	"io"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
)

// IndexArchiveSource loads a message's raw RFC 822 from its archiveRef (P5.1).
// IMAP mailbox fetch is wired via archive.Service in cmd (HOTSPOTS).
type IndexArchiveSource struct {
	DB      *store.DB
	Objects RawStore
}

type archiveMsg struct {
	ID         string `bson:"_id"`
	AccountID  string `bson:"accountId"`
	ArchiveRef string `bson:"archiveRef,omitempty"`
}

// Raw implements MessageSource.
func (s *IndexArchiveSource) Raw(ctx context.Context, accountID, messageID string) ([]byte, error) {
	if s == nil || s.DB == nil || s.Objects == nil {
		return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.")
	}
	if !store.IsID(store.PrefixMessage, messageID) {
		return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.").WithSafe("messageId", messageID)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": messageID, "accountId": accountID})
	if err != nil {
		return nil, err
	}
	var m archiveMsg
	err = s.DB.Collection(index.Messages).FindOne(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = s.DB.Collection(index.ArchivedMessages).FindOne(ctx, filter).Decode(&m)
	}
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.").WithSafe("messageId", messageID)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.reference.message")
	}
	if m.ArchiveRef == "" {
		return nil, errs.New(errs.CodeMessageNotFound, "The message is gone from the mailbox and was not archived.").
			WithSafe("messageId", messageID)
	}
	rc, _, err := s.Objects.Get(ctx, m.ArchiveRef)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "submit.reference.archive")
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}
