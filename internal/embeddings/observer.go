package embeddings

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/fulltext"
)

// The Service is a fulltext.Observer (and AccountObserver): the full-text indexer tells it about every write
// to the text store. The calls are cheap: Indexed only queues the message, the deletes are single
// DeleteMany calls.
var (
	_ fulltext.Observer        = (*Service)(nil)
	_ fulltext.AccountObserver = (*Service)(nil)
)

// observerTimeout bounds the deletes done inside an indexer callback.
const observerTimeout = 10 * time.Second

// Indexed queues the message for embedding (never blocks; the worker checks the switches).
func (s *Service) Indexed(_ context.Context, row *fulltext.Row) {
	if row == nil || row.OrganizationID == "" || row.AccountID == "" || row.MessageID == "" {
		return
	}
	s.enqueue(task{org: row.OrganizationID, acc: row.AccountID, msg: row.MessageID})
}

// Removed deletes the vectors of removed text-store rows: messages, a folder, or the whole account.
func (s *Service) Removed(ctx context.Context, r fulltext.Removal) {
	if r.AccountID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observerTimeout)
	defer cancel()
	var err error
	switch {
	case len(r.MessageIDs) > 0:
		_, err = s.Repo.DeleteMessages(ctx, r.OrganizationID, r.AccountID, r.MessageIDs)
	case r.MailboxID != "":
		_, err = s.Repo.DeleteMailbox(ctx, r.OrganizationID, r.AccountID, r.MailboxID)
	default:
		_, err = s.PurgeAccount(ctx, r.AccountID)
	}
	if err != nil {
		// The sweep and the lazy cleanup at search time remove them later; search never shows them (it joins
		// the text store).
		s.log().WarnContext(ctx, "deleting embeddings failed", "accountId", r.AccountID, "err", err)
	}
}

// AccountChanged re-resolves the switch after the account's switches changed and purges its vectors at
// once when semantic search is no longer available for it.
func (s *Service) AccountChanged(ctx context.Context, org, acc string) {
	if s.Switches != nil {
		s.Switches.Forget(acc)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), observerTimeout)
	defer cancel()
	reason, _, err := s.state(ctx, org, acc, true)
	if err != nil {
		s.log().WarnContext(ctx, "embeddings switch check failed; the sweep retries", "accountId", acc, "err", err)
		return
	}
	if reason == ReasonNone || reason == ReasonNotConfigured {
		return // not_configured is an organisation setting: the model-change hook and the sweep handle it
	}
	if _, err := s.PurgeAccount(ctx, acc); err != nil {
		s.log().WarnContext(ctx, "embeddings purge failed; the sweep retries", "accountId", acc, "err", err)
	}
}
