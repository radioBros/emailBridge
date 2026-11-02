package fulltext

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// defaultMinInterval spaces the body fetches of one account on one node (indexer and reindex job share it),
// so indexing never monopolises the account's single on-demand connection.
const defaultMinInterval = 250 * time.Millisecond

// TextFetcher fetches the body text of an indexed message from the account's provider.
type TextFetcher interface {
	FetchText(ctx context.Context, accountID, messageID string) (plain, html string, err error)
}

// MessagesFetcher is the TextFetcher over messages.Service (IMAP, Gmail and Graph alike). The \Seen flag is
// never touched.
type MessagesFetcher struct {
	Svc *messages.Service
}

// FetchText implements TextFetcher.
func (f MessagesFetcher) FetchText(ctx context.Context, accountID, messageID string) (string, string, error) {
	v, err := f.Svc.GetMessage(ctx, accountID, messageID, messages.GetOptions{Text: "both", MaxBytes: fetchBytes})
	if err != nil {
		return "", "", err
	}
	if v == nil {
		return "", "", nil
	}
	return v.Text, v.HTML, nil
}

// Service is the full-text indexer, search and reindex job.
type Service struct {
	DB       *store.DB
	Repo     *Repo
	Switches *Switches
	Fetcher  TextFetcher // nil on nodes without the api role: nothing is indexed there
	Jobs     *jobs.Service
	Audit    *audit.Writer
	Log      *slog.Logger
	Now      func() time.Time
	// MinInterval spaces body fetches per account (default 250 ms; negative = no pacing, tests).
	MinInterval time.Duration
	// PurgeEvery is the purge sweep interval (default 15 min).
	PurgeEvery time.Duration
	// Observers are told about every write (extension point, see Observer). Set it before Start; a module
	// that starts later registers with AddObserver.
	Observers []Observer

	obsMu  sync.RWMutex
	turnMu sync.Mutex
	turns  map[string]*turn
}

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) minInterval() time.Duration {
	if s.MinInterval == 0 {
		return defaultMinInterval
	}
	return s.MinInterval
}

// systemCtx is the context of background work for an organisation: tenant scope, a system caller (the
// messages service checks read access) and the correlation IDs.
func systemCtx(ctx context.Context, orgID, accountID string) context.Context {
	ctx = store.WithOrganization(ctx, orgID)
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: orgID})
	return logging.With(ctx, func(ids *logging.IDs) { ids.OrganizationID, ids.AccountID = orgID, accountID })
}

// turn is the per-account fetch gate of this node: one fetch at a time, MinInterval between two.
type turn struct {
	slot  chan struct{}
	next  time.Time
	users int
}

// acquireTurn waits for the account's turn; release ends it. The indexer and the reindex job share the
// gate, so together they keep to one fetch at a time and leave the account's single on-demand connection
// free between fetches for interactive reads.
func (s *Service) acquireTurn(ctx context.Context, accountID string) (release func(), err error) {
	gap := s.minInterval()
	if gap < 0 {
		return func() {}, nil
	}
	s.turnMu.Lock()
	if s.turns == nil {
		s.turns = map[string]*turn{}
	}
	t := s.turns[accountID]
	if t == nil {
		t = &turn{slot: make(chan struct{}, 1)}
		s.turns[accountID] = t
	}
	t.users++
	s.turnMu.Unlock()
	leave := func() {
		s.turnMu.Lock()
		t.users--
		if t.users == 0 {
			delete(s.turns, accountID)
		}
		s.turnMu.Unlock()
	}
	select {
	case t.slot <- struct{}{}:
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
	if wait := time.Until(t.next); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			<-t.slot
			leave()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return func() {
		t.next = time.Now().Add(gap)
		<-t.slot
		leave()
	}, nil
}

// IndexMessage fetches the text of one indexed message and upserts its row. It reports false without an
// error when there is nothing to do: the switch is off for the account, the account is missing or being
// deleted, or the message left the index. Provider errors are returned for the caller to classify.
func (s *Service) IndexMessage(ctx context.Context, orgID, accountID, messageID string) (bool, error) {
	if s.Fetcher == nil {
		return false, errs.New(errs.CodeInternalError, "Full-text indexing runs on api nodes only.")
	}
	ctx = systemCtx(ctx, orgID, accountID)
	on, err := s.Switches.AccountEnabled(ctx, orgID, accountID)
	if err != nil {
		return false, errs.Wrap(err, errs.CodeInternalError, "fulltext.switch")
	}
	if !on {
		return false, nil
	}
	msg, err := s.indexRow(ctx, orgID, accountID, messageID)
	if err != nil || msg == nil {
		return false, err
	}
	release, err := s.acquireTurn(ctx, accountID)
	if err != nil {
		return false, err
	}
	plain, htmlBody, err := s.Fetcher.FetchText(ctx, accountID, messageID)
	release()
	if err != nil {
		return false, err
	}
	text, truncated := bodyText(plain, htmlBody)
	row := &Row{
		OrganizationID: orgID, AccountID: accountID, MessageID: messageID, MailboxID: msg.MailboxID,
		Path: s.mailboxPath(ctx, accountID, msg.MailboxID), Subject: msg.Subject, From: addrString(msg.From),
		To: addrStrings(msg.To), Date: msg.InternalDate.UTC(), Text: text, TextTruncated: truncated, Size: msg.Size,
		IndexedAt: s.now().Truncate(time.Millisecond),
	}
	if msg.Date != nil && !msg.Date.IsZero() {
		row.Date = msg.Date.UTC()
	}
	if err := s.Repo.Upsert(ctx, row); err != nil {
		return false, err
	}
	// The message may have been deleted while its body was being fetched: the delete event found no row
	// then, so take the row back out now. (A switch turned off meanwhile is caught by the purge sweep.)
	if gone, err := s.indexRow(ctx, orgID, accountID, messageID); err == nil && gone == nil {
		if _, err := s.Repo.DeleteMessages(ctx, orgID, accountID, []string{messageID}); err != nil {
			return false, err
		}
		s.removed(ctx, Removal{OrganizationID: orgID, AccountID: accountID, MessageIDs: []string{messageID}})
		return false, nil
	}
	for _, o := range s.observers() {
		o.Indexed(ctx, row)
	}
	return true, nil
}

// indexRow loads the message from the message index (nil when it is not there).
func (s *Service) indexRow(ctx context.Context, orgID, accountID, messageID string) (*index.Message, error) {
	var m index.Message
	err := s.DB.Collection(index.Messages).FindOne(ctx, bson.M{"_id": messageID, "accountId": accountID, "organizationId": orgID}).Decode(&m)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "fulltext.index_row")
	}
	return &m, nil
}

func (s *Service) mailboxPath(ctx context.Context, accountID, mailboxID string) string {
	var mb struct {
		Path string `bson:"path"`
	}
	if err := s.DB.Collection(index.Mailboxes).FindOne(ctx, bson.M{"_id": mailboxID, "accountId": accountID},
		options.FindOne().SetProjection(bson.M{"path": 1})).Decode(&mb); err != nil {
		return ""
	}
	return mb.Path
}

// AddObserver registers an observer while the indexer may already be running (a module that starts after
// this one, e.g. G1 embeddings).
func (s *Service) AddObserver(o Observer) {
	s.obsMu.Lock()
	defer s.obsMu.Unlock()
	s.Observers = append(s.Observers, o)
}

// observers is a snapshot of the registered observers.
func (s *Service) observers() []Observer {
	s.obsMu.RLock()
	defer s.obsMu.RUnlock()
	return append([]Observer(nil), s.Observers...)
}

func (s *Service) removed(ctx context.Context, r Removal) {
	for _, o := range s.observers() {
		o.Removed(ctx, r)
	}
}

// accountChanged tells the observers that implement AccountObserver that an account's switches changed.
func (s *Service) accountChanged(ctx context.Context, orgID, accountID string) {
	for _, o := range s.observers() {
		if ao, ok := o.(AccountObserver); ok {
			ao.AccountChanged(ctx, orgID, accountID)
		}
	}
}

// PurgeAccount deletes every row of an account.
func (s *Service) PurgeAccount(ctx context.Context, orgID, accountID string) (int64, error) {
	n, err := s.Repo.DeleteAccount(ctx, accountID)
	if err != nil {
		return 0, err
	}
	s.Switches.Forget(accountID)
	if n > 0 {
		s.log().InfoContext(ctx, "full-text rows of the account purged", "accountId", accountID, "rows", n)
		s.removed(ctx, Removal{OrganizationID: orgID, AccountID: accountID})
	}
	return n, nil
}
