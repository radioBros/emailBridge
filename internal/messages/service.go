package messages

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// AttachmentLister reads attachment index rows for a message (W50 / G10).
type AttachmentLister interface {
	ListByMessage(ctx context.Context, accountID, messageID string) ([]AttachmentView, error)
}

// Service implements the mailbox/messages/drafts API (P4.1 + W50).
type Service struct {
	DB          *store.DB
	Pool        *ondemand.Pool
	Accounts    AccountLoader
	Submit      Submitter
	Attachments AttachmentLister
	Providers   OpsFactory
	Log         *slog.Logger
	Now         func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) loadAccount(ctx context.Context, accountID string) (*ebsync.Account, error) {
	if !store.IsID(store.PrefixAccount, accountID) {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
	}
	acc, err := s.Accounts.Load(ctx, accountID)
	if err != nil {
		return nil, err
	}
	// The account source is not tenant-scoped: every live-mailbox operation (IMAP, Gmail, Graph) must only
	// reach the caller's own organisation's accounts. Same answer as a missing account.
	if org, ok := store.OrganizationFrom(ctx); !ok || acc == nil || acc.OrganizationID != org {
		return nil, errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
	}
	return acc, nil
}

func (s *Service) withIMAP(ctx context.Context, accountID string, fn func(cl *imapc.Client, acc *ebsync.Account) error) error {
	acc, err := s.loadAccount(ctx, accountID)
	if err != nil {
		return err
	}
	lease, err := s.Pool.Acquire(ctx, acc)
	if err != nil {
		return ondemand.AcquireErr(err)
	}
	err = fn(lease.Client, acc)
	if err != nil {
		lease.Discard()
		return err
	}
	lease.Release()
	return nil
}

func (s *Service) loadMailbox(ctx context.Context, accountID, mailboxID string) (*mbxDoc, error) {
	if !store.IsID(store.PrefixMailbox, mailboxID) {
		return nil, errs.New(errs.CodeMailboxNotFound, "The mailbox does not exist.").WithSafe("mailboxId", mailboxID)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": mailboxID, "accountId": accountID, "status": "active"})
	if err != nil {
		return nil, err
	}
	var m mbxDoc
	err = s.DB.Collection(index.Mailboxes).FindOne(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeMailboxNotFound, "The mailbox does not exist.").WithSafe("mailboxId", mailboxID)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.mailbox")
	}
	return &m, nil
}

func (s *Service) loadMailboxByPath(ctx context.Context, accountID, path string) (*mbxDoc, error) {
	filter, err := store.Scoped(ctx, bson.M{"accountId": accountID, "path": path, "status": "active"})
	if err != nil {
		return nil, err
	}
	var m mbxDoc
	err = s.DB.Collection(index.Mailboxes).FindOne(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeMailboxNotFound, "The mailbox does not exist.").WithSafe("path", path)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.mailbox.path")
	}
	return &m, nil
}

func (s *Service) resolveMailbox(ctx context.Context, accountID, mailboxID, path string) (*mbxDoc, error) {
	if mailboxID != "" {
		return s.loadMailbox(ctx, accountID, mailboxID)
	}
	if path != "" {
		return s.loadMailboxByPath(ctx, accountID, path)
	}
	return nil, errs.New(errs.CodeValidationFailed, "mailboxId or path is required.")
}

func (s *Service) loadMessage(ctx context.Context, accountID, messageID string) (*msgDoc, error) {
	if !store.IsID(store.PrefixMessage, messageID) {
		return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.").WithSafe("messageId", messageID)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": messageID, "accountId": accountID})
	if err != nil {
		return nil, err
	}
	var m msgDoc
	err = s.DB.Collection(index.Messages).FindOne(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.").WithSafe("messageId", messageID)
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "messages.message")
	}
	return &m, nil
}

func (s *Service) selectChecked(ctx context.Context, cl *imapc.Client, path string, expectUV uint32) (*imapc.SelectInfo, error) {
	info, err := cl.Select(ctx, path, false)
	if err != nil {
		return nil, err
	}
	if expectUV != 0 && info.UIDValidity != expectUV {
		return nil, errs.New(errs.CodeUidValidityMismatch,
			"The mailbox UIDVALIDITY changed; refresh and retry.").
			WithSafe("expected", expectUV).WithSafe("actual", info.UIDValidity)
	}
	return info, nil
}

func decodeRaw(b64 string) ([]byte, error) {
	if b64 == "" {
		return nil, errs.New(errs.CodeValidationFailed, "raw is required.")
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		raw, err = base64.RawStdEncoding.DecodeString(b64)
	}
	if err != nil {
		return nil, errs.New(errs.CodeValidationFailed, "raw must be base64.")
	}
	if int64(len(raw)) > maxUpload {
		return nil, errs.New(errs.CodeMessageTooLarge,
			"The message exceeds the upload limit.").WithSafe("limit", maxUpload)
	}
	return raw, nil
}

func mbxView(m *mbxDoc) MailboxView {
	return MailboxView{
		ID: m.ID, AccountID: m.AccountID, Path: m.Path, Delimiter: m.Delimiter,
		SpecialUse: m.SpecialUse, UIDValidity: m.UIDValidity, UIDNext: m.UIDNext,
		HighestModSeq: m.HighestModSeq, Exists: m.Exists, SyncMode: m.SyncMode,
		LastSyncAt: m.LastSyncAt, Status: m.Status,
	}
}

func msgView(m *msgDoc) MessageView {
	flags := m.Flags
	if flags == nil {
		flags = []string{}
	}
	return MessageView{
		ID: m.ID, AccountID: m.AccountID, MailboxID: m.MailboxID,
		UIDValidity: m.UIDValidity, UID: m.UID, ModSeq: m.ModSeq, Flags: flags,
		MessageIDHeader: m.MessageIDHeader, EmailID: m.EmailID, ThreadID: m.ThreadID,
		InternalDate: m.InternalDate, Size: m.Size, Subject: m.Subject, From: addrViews(m.From), To: addrList(m.To),
		Date: m.Date,
	}
}

func addrViews(a *addrDoc) []AddressView {
	if a == nil {
		return nil
	}
	return []AddressView{{Name: a.Name, Address: a.Address}}
}

func addrList(in []addrDoc) []AddressView {
	out := make([]AddressView, 0, len(in))
	for _, a := range in {
		out = append(out, AddressView(a))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func draftView(d *draftDoc) DraftView {
	return DraftView{
		ID: d.ID, AccountID: d.AccountID, Path: d.Path, UID: d.UID, UIDValidity: d.UIDValidity,
		MessageIDHeader: d.MessageIDHeader, Subject: d.Subject, Size: d.Size,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

func chunkUIDs(uids []uint32, n int) [][]uint32 {
	if n <= 0 {
		n = uidChunk
	}
	var out [][]uint32
	for len(uids) > 0 {
		c := n
		if c > len(uids) {
			c = len(uids)
		}
		out = append(out, uids[:c])
		uids = uids[c:]
	}
	return out
}

// requireOwnAccount answers "not found" for an account of another organisation on index-only reads (lists),
// which are tenant-filtered but would otherwise answer an empty 200 for a foreign account. One indexed count.
func (s *Service) requireOwnAccount(ctx context.Context, accountID string) error {
	notFound := errs.New(errs.CodeNotFound, "The account does not exist.").WithSafe("accountId", accountID)
	if !store.IsID(store.PrefixAccount, accountID) || s.DB == nil {
		return notFound
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": accountID})
	if err != nil {
		return err
	}
	n, err := s.DB.Collection("accounts").CountDocuments(ctx, filter, options.Count().SetLimit(1))
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "messages.account_owner")
	}
	if n == 0 {
		return notFound
	}
	return nil
}
