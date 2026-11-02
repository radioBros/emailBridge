package messages

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

const attachmentsColl = "attachments"

type attDoc struct {
	ID          string `bson:"_id"`
	AccountID   string `bson:"accountId"`
	MessageID   string `bson:"messageId"`
	Filename    string `bson:"filename,omitempty"`
	ContentType string `bson:"contentType"`
	Size        int64  `bson:"size"`
	ContentID   string `bson:"contentId,omitempty"`
	Disposition string `bson:"disposition,omitempty"`
	Scan        string `bson:"scan,omitempty"`
	ObjectKey   string `bson:"objectKey,omitempty"`
}

// MongoAttachmentLister reads the attachments collection (W48 rows; W50 read-only).
type MongoAttachmentLister struct {
	DB *store.DB
}

// ListByMessage implements AttachmentLister.
func (l *MongoAttachmentLister) ListByMessage(ctx context.Context, accountID, messageID string) ([]AttachmentView, error) {
	if l == nil || l.DB == nil {
		return nil, nil
	}
	filter, err := store.Scoped(ctx, bson.M{"accountId": accountID, "messageId": messageID})
	if err != nil {
		return nil, err
	}
	cur, err := l.DB.Collection(attachmentsColl).Find(ctx, filter)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.attachments.list")
	}
	var rows []attDoc
	if err := cur.All(ctx, &rows); err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.attachments.list")
	}
	out := make([]AttachmentView, len(rows))
	for i, r := range rows {
		out[i] = AttachmentView{
			ID: r.ID, Filename: r.Filename, ContentType: r.ContentType, Size: r.Size,
			ContentID: r.ContentID, Inline: strings.EqualFold(r.Disposition, "inline") && r.ContentID != "",
			Scan: r.Scan,
		}
	}
	return out, nil
}

func (s *Service) loadAttachments(ctx context.Context, accountID, messageID string) ([]AttachmentView, error) {
	if s.Attachments == nil {
		return nil, nil
	}
	return s.Attachments.ListByMessage(ctx, accountID, messageID)
}
