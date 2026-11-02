package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// AccountLoader loads decrypted IMAP settings (wired to accounts.SyncSource).
type AccountLoader interface {
	Load(ctx context.Context, accountID string) (*ebsync.Account, error)
}

// Source of a served raw body.
const (
	SourceMailbox = "mailbox"
	SourceArchive = "archive"
)

// Service serves raw sources and runs retention.
type Service struct {
	DB       *store.DB
	Store    *Store
	Pool     *Pool
	Accounts AccountLoader
	Log      *slog.Logger

	// RetentionEvery is how often the duty ticks (default 1h; tests shorten it).
	RetentionEvery time.Duration
	// Now overrides time.Now in tests.
	Now func() time.Time
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.Default()
	}
	return s.Log
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

type msgDoc struct {
	ID             string    `bson:"_id"`
	OrganizationID string    `bson:"organizationId"`
	AccountID      string    `bson:"accountId"`
	MailboxID      string    `bson:"mailboxId"`
	UIDValidity    uint32    `bson:"uidValidity"`
	UID            uint32    `bson:"uid"`
	InternalDate   time.Time `bson:"internalDate"`
	ArchiveRef     string    `bson:"archiveRef,omitempty"`
	// Gone is set when the message left the mailbox and only its archive tombstone remains.
	Gone bool `bson:"-"`
}

type mailboxDoc struct {
	ID   string `bson:"_id"`
	Path string `bson:"path"`
}

func (s *Service) loadMessage(ctx context.Context, accountID, messageID string) (*msgDoc, error) {
	if !store.IsID(store.PrefixMessage, messageID) {
		return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.").WithSafe("messageId", messageID)
	}
	filter, err := store.Scoped(ctx, bson.M{"_id": messageID, "accountId": accountID})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "archive.scoped")
	}
	var m msgDoc
	err = s.DB.Collection(index.Messages).FindOne(ctx, filter).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		// Gone from the mailbox: an archived message keeps a tombstone with its archive reference.
		err = s.DB.Collection(index.ArchivedMessages).FindOne(ctx, filter).Decode(&m)
		m.Gone = err == nil
	}
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errs.New(errs.CodeMessageNotFound, "The message does not exist.").WithSafe("messageId", messageID)
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "archive.message")
	}
	return &m, nil
}

func (s *Service) mailboxPath(ctx context.Context, mailboxID string) (string, error) {
	var m mailboxDoc
	if err := s.DB.Collection(index.Mailboxes).FindOne(ctx, bson.M{"_id": mailboxID}).Decode(&m); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return "", errs.New(errs.CodeMessageNotFound, "The message mailbox is gone.")
		}
		return "", errs.Wrap(err, errs.CodeInternalError, "archive.mailbox")
	}
	return m.Path, nil
}

// rawResult is a resolved body ready to stream.
type rawResult struct {
	Source string
	Body   []byte        // set for mailbox (IMAP literal already buffered)
	Reader io.ReadCloser // set for archive stream; caller closes after copy
	Size   int64
}

// resolveRaw fetches from the mailbox first, falling back to the archive.
func (s *Service) resolveRaw(ctx context.Context, accountID, messageID string) (*rawResult, error) {
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	if m.Gone {
		return s.fromArchive(ctx, m.ArchiveRef)
	}
	if body, ok := s.fromMailbox(ctx, accountID, m); ok {
		return &rawResult{Source: SourceMailbox, Body: body, Size: int64(len(body))}, nil
	}
	if m.ArchiveRef == "" {
		return nil, errs.New(errs.CodeMessageNotFound, "The message is gone from the mailbox and was not archived.")
	}
	return s.fromArchive(ctx, m.ArchiveRef)
}

// resolveArchive serves the archive copy only.
func (s *Service) resolveArchive(ctx context.Context, accountID, messageID string) (*rawResult, error) {
	m, err := s.loadMessage(ctx, accountID, messageID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeMessageNotFound {
			return nil, errs.New(errs.CodeArchiveNotFound, "Archive not found.").WithSafe("messageId", messageID)
		}
		return nil, err
	}
	if m.ArchiveRef == "" {
		return nil, errs.New(errs.CodeArchiveNotFound, "Archive not found.").WithSafe("messageId", messageID)
	}
	res, err := s.fromArchive(ctx, m.ArchiveRef)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) || errs.CodeOf(err) == errs.CodeArchiveNotFound {
			return nil, errs.New(errs.CodeArchiveNotFound, "Archive not found.").WithSafe("messageId", messageID)
		}
		return nil, err
	}
	return res, nil
}

func (s *Service) fromArchive(ctx context.Context, key string) (*rawResult, error) {
	rc, info, err := s.Store.Open(ctx, key)
	if err != nil {
		if errors.Is(err, objstore.ErrNotFound) {
			return nil, errs.New(errs.CodeArchiveNotFound, "Archive not found.")
		}
		return nil, errs.Wrap(err, errs.CodeInternalError, "archive.get")
	}
	return &rawResult{Source: SourceArchive, Reader: rc, Size: info.Size}, nil
}

// fromMailbox tries IMAP; false means fall back to archive (gone / UIDVALIDITY / dial).
func (s *Service) fromMailbox(ctx context.Context, accountID string, m *msgDoc) ([]byte, bool) {
	if s.Accounts == nil || s.Pool == nil {
		return nil, false
	}
	path, err := s.mailboxPath(ctx, m.MailboxID)
	if err != nil {
		return nil, false
	}
	acc, err := s.Accounts.Load(ctx, accountID)
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelWarn, "archive: load account for raw", slog.Any("err", err))
		return nil, false
	}
	lease, err := s.Pool.Acquire(ctx, acc)
	if err != nil {
		s.log().LogAttrs(ctx, slog.LevelWarn, "archive: on-demand IMAP acquire", slog.Any("err", err))
		return nil, false
	}
	info, err := lease.Client.Select(ctx, path, true)
	if err != nil {
		lease.Discard()
		return nil, false
	}
	if info.UIDValidity != m.UIDValidity {
		lease.Release()
		return nil, false
	}
	raw, err := lease.Client.Raw(ctx, m.UID, 0)
	if err != nil {
		lease.Discard()
		return nil, false
	}
	lease.Release()
	return raw, true
}

// OpenRaw opens a message's raw source the way GET …/messages/{id}/raw does (mailbox first, then the archive),
// for other packages that serve a raw message (PEC receipts, S17 §8). The caller closes the reader.
func (s *Service) OpenRaw(ctx context.Context, accountID, messageID string) (io.ReadCloser, error) {
	res, err := s.resolveRaw(ctx, accountID, messageID)
	if err != nil {
		return nil, err
	}
	if res.Reader != nil {
		return res.Reader, nil
	}
	return io.NopCloser(bytes.NewReader(res.Body)), nil
}
