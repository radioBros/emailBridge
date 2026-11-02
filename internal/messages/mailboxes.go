package messages

import (
	"context"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// ListMailboxes returns active indexed mailboxes for an account.
func (s *Service) ListMailboxes(ctx context.Context, accountID, path string, limit int, cursor string) ([]MailboxView, *string, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, nil, err
	}
	if err := s.requireOwnAccount(ctx, accountID); err != nil {
		return nil, nil, err
	}
	if limit <= 0 || limit > maxMbxList {
		limit = maxMbxList
	}
	filter := bson.M{"accountId": accountID, "status": "active"}
	if path != "" {
		filter["path"] = path
	}
	filter, err := store.Scoped(ctx, filter)
	if err != nil {
		return nil, nil, err
	}
	if cursor != "" {
		cur, err := decodeCursor(cursor)
		if err != nil {
			return nil, nil, errs.New(errs.CodeValidationFailed, "The cursor is invalid.")
		}
		filter["_id"] = bson.M{"$gt": cur.ID}
	}
	opts := options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(limit + 1))
	c, err := s.DB.Collection(index.Mailboxes).Find(ctx, filter, opts)
	if err != nil {
		return nil, nil, errs.Wrap(err, errs.CodeInternalError, "messages.mailboxes.list")
	}
	defer func() { _ = c.Close(ctx) }()
	var rows []mbxDoc
	if err := c.All(ctx, &rows); err != nil {
		return nil, nil, errs.Wrap(err, errs.CodeInternalError, "messages.mailboxes.list")
	}
	var next *string
	if len(rows) > limit {
		rows = rows[:limit]
		nc := encodeCursor(cursorPayload{ID: rows[len(rows)-1].ID})
		next = &nc
	}
	out := make([]MailboxView, len(rows))
	for i := range rows {
		out[i] = mbxView(&rows[i])
	}
	return out, next, nil
}

// GetMailbox returns one indexed mailbox.
func (s *Service) GetMailbox(ctx context.Context, accountID, mailboxID string) (*MailboxView, error) {
	if err := tenancy.RequireReadAccount(ctx, "messages", accountID); err != nil {
		return nil, err
	}
	m, err := s.loadMailbox(ctx, accountID, mailboxID)
	if err != nil {
		return nil, err
	}
	v := mbxView(m)
	return &v, nil
}

// CreateMailbox creates a folder on the provider.
func (s *Service) CreateMailbox(ctx context.Context, accountID string, in CreateMailboxInput) (*MailboxView, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, errs.New(errs.CodeValidationFailed, "path is required.")
	}
	sub := true
	if in.Subscribe != nil {
		sub = *in.Subscribe
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return nil, err
	}
	if err := ops.CreateMailbox(ctx, in.Path, sub); err != nil {
		return nil, err
	}
	if m, err := s.loadMailboxByPath(ctx, accountID, in.Path); err == nil {
		v := mbxView(m)
		return &v, nil
	}
	return &MailboxView{AccountID: accountID, Path: in.Path, Status: "pending"}, nil
}

// PatchMailbox renames and/or (un)subscribes a mailbox.
func (s *Service) PatchMailbox(ctx context.Context, accountID, mailboxID string, in PatchMailboxInput) (*MailboxView, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return nil, err
	}
	m, err := s.loadMailbox(ctx, accountID, mailboxID)
	if err != nil {
		return nil, err
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return nil, err
	}
	path := m.Path
	if in.Path != nil && *in.Path != "" && *in.Path != m.Path {
		if err := ops.RenameMailbox(ctx, m.Path, *in.Path); err != nil {
			return nil, err
		}
		path = *in.Path
	}
	if in.Subscribed != nil {
		if err := ops.Subscribe(ctx, path, *in.Subscribed); err != nil {
			return nil, err
		}
	}
	if updated, err := s.loadMailbox(ctx, accountID, mailboxID); err == nil {
		v := mbxView(updated)
		if in.Path != nil {
			v.Path = path
		}
		return &v, nil
	}
	v := mbxView(m)
	v.Path = path
	return &v, nil
}

// DeleteMailbox deletes a folder on the provider.
func (s *Service) DeleteMailbox(ctx context.Context, accountID, mailboxID string) error {
	if err := tenancy.RequireAccount(ctx, tenancy.PermMessagesWrite, accountID); err != nil {
		return err
	}
	m, err := s.loadMailbox(ctx, accountID, mailboxID)
	if err != nil {
		return err
	}
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return err
	}
	ops, err := s.opsFor(ctx, acc)
	if err != nil {
		return err
	}
	return ops.DeleteMailbox(ctx, m.Path)
}
