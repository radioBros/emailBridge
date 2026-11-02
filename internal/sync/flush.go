package sync

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/index"
)

// flushAccount drops local sync index state for the account (not the archive) and clears in-memory
// mailboxes so the next pass rebuilds silently (no message.received for existing mail). W48 / G15.
func (r *runner) flushAccount(ctx context.Context) error {
	r.log.LogAttrs(ctx, slog.LevelInfo, "sync: flush account index",
		slog.String("account_id", r.acc.ID))
	mailboxes, err := r.ix.Mailboxes(ctx, r.acc.ID)
	if err != nil {
		return err
	}
	var allIDs []string
	for _, m := range mailboxes {
		known, _ := r.ix.MessageStates(ctx, m.ID)
		for _, e := range known {
			allIDs = append(allIDs, e.ID)
		}
		if err := r.ix.Fenced(ctx, r.acc.ID, r.epoch, func(tx context.Context) error {
			if _, err := r.ix.DeleteMailbox(tx, m); err != nil {
				return err
			}
			return nil
		}); err != nil {
			return err
		}
	}
	r.eraseAttachmentsBestEffort(ctx, allIDs)
	r.mailboxes = map[string]*index.Mailbox{}
	r.lastFull = map[string]time.Time{}
	r.initialized = false
	return nil
}
