package fulltext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
)

const (
	// maxDeliveries is how often one event is tried before the indexer gives up on it (the message can
	// still be indexed later by a reindex job).
	maxDeliveries = 6
	retryBase     = 5 * time.Second
	retryMax      = 2 * time.Minute
	// busyRetry is the delay after connection_busy / rate_limited (someone else is using the account's
	// on-demand connection right now).
	busyRetry = 2 * time.Second
	// consumerAckPending is how many events of one partition are in flight: a little parallelism so one
	// slow or retried account does not hold up the others; per-account order is not needed for correctness
	// (IndexMessage re-checks the index after writing, search drops rows whose message left the index).
	consumerAckPending = 4
)

// messageEventData is the part of message.* and mailbox.* event data the indexer reads.
type messageEventData struct {
	AccountID string `json:"accountId"`
	MessageID string `json:"messageId"`
	Mailbox   struct {
		MailboxID string `json:"mailboxId"`
	} `json:"mailbox"`
}

// accountUpdatedData is the part of account.updated the indexer reads.
type accountUpdatedData struct {
	Changed []string `json:"changed"`
}

// HandleEvent applies one event to the text store. Unrelated events are ignored. The returned error is the
// processing error; PartitionWork classifies it into retry or give-up.
func (s *Service) HandleEvent(ctx context.Context, ev events.Event) error {
	org, acc := ev.EBOrg, ev.EBAccount
	switch ev.Type {
	case events.MessageReceived, events.MessageDeleted, events.MailboxDeleted, events.MailboxReset:
	case events.AccountUpdated, events.AccountDeleted:
	default:
		return nil
	}
	var d messageEventData
	if len(ev.Data) > 0 {
		if err := json.Unmarshal(ev.Data, &d); err != nil {
			return bus.DiscardMessage(fmt.Errorf("fulltext: event data: %w", err))
		}
	}
	if acc == "" {
		acc = d.AccountID
	}
	if org == "" || acc == "" {
		return nil
	}
	ctx = systemCtx(ctx, org, acc)
	switch ev.Type {
	case events.MessageReceived:
		if d.MessageID == "" {
			return nil
		}
		_, err := s.IndexMessage(ctx, org, acc, d.MessageID)
		return err
	case events.MessageDeleted:
		if d.MessageID == "" {
			return nil
		}
		n, err := s.Repo.DeleteMessages(ctx, org, acc, []string{d.MessageID})
		if err == nil && n > 0 {
			s.removed(ctx, Removal{OrganizationID: org, AccountID: acc, MessageIDs: []string{d.MessageID}})
		}
		return err
	case events.MailboxDeleted, events.MailboxReset:
		// After a reset (UIDVALIDITY changed) the folder's messages get new IDs without message.received; a
		// reindex job brings them back.
		if d.Mailbox.MailboxID == "" {
			return nil
		}
		n, err := s.Repo.DeleteMailbox(ctx, org, acc, d.Mailbox.MailboxID)
		if err == nil && n > 0 {
			s.removed(ctx, Removal{OrganizationID: org, AccountID: acc, MailboxID: d.Mailbox.MailboxID})
		}
		return err
	case events.AccountUpdated:
		var u accountUpdatedData
		_ = json.Unmarshal(ev.Data, &u)
		if !slices.Contains(u.Changed, "switches") {
			return nil
		}
		s.Switches.Forget(acc)
		s.accountChanged(ctx, org, acc)
		on, err := s.Switches.AccountEnabledFresh(ctx, org, acc)
		if err != nil {
			return errs.Wrap(err, errs.CodeInternalError, "fulltext.switch")
		}
		if !on {
			_, err = s.PurgeAccount(ctx, org, acc)
		}
		return err
	default: // account.deleted: erasure already removed the rows; this catches rows written during it
		_, err := s.PurgeAccount(ctx, org, acc)
		return err
	}
}

// classify turns a processing error into the consumer's answer: nil (ack), a delayed retry, or a discard.
// Errors that cannot get better by waiting (the message is gone, permanent provider errors) are acked; the
// others are retried with back-off up to maxDeliveries, then acked with a warning. Nothing here ever holds
// up the sync engine: the indexer has its own durable consumers.
func (s *Service) classify(ctx context.Context, ev events.Event, err error, delivered uint64) error {
	if err == nil {
		return nil
	}
	var discard *bus.Discard
	if errors.As(err, &discard) {
		s.log().ErrorContext(ctx, "discarding malformed event for full-text indexing", "eventId", ev.ID, "type", ev.Type, "err", err)
		return err
	}
	attrs := []any{"eventId", ev.ID, "type", ev.Type, "code", string(errs.CodeOf(err)), "attempt", delivered}
	kind := errs.KindOf(err)
	switch kind {
	case errs.KindNotFound, errs.KindValidation, errs.KindForbidden, errs.KindPermanent, errs.KindConfig,
		errs.KindConflict, errs.KindAuth:
		// Gone, refused, or the account cannot log in (its health page says so): waiting does not help.
		s.log().InfoContext(ctx, "full-text indexing skipped a message", append(attrs, "err", err)...)
		return nil
	}
	if delivered >= maxDeliveries {
		s.log().WarnContext(ctx, "full-text indexing gave up on a message; a reindex can add it later", append(attrs, "err", err)...)
		return nil
	}
	delay := retryDelay(delivered)
	if kind == errs.KindRateLimited {
		delay = busyRetry
	}
	s.log().DebugContext(ctx, "full-text indexing will retry", append(attrs, "err", err, "delay", delay.String())...)
	return bus.DelayRetry(delay, err)
}

// retryDelay is retryBase doubled per earlier delivery, capped at retryMax.
func retryDelay(delivered uint64) time.Duration {
	d := retryBase
	for i := uint64(1); i < delivered && d < retryMax; i++ {
		d *= 2
	}
	return min(d, retryMax)
}

// PartitionWork is bus.Balancer.Work for the indexer: one durable consumer per event partition
// (fulltext-<p>) with consumerAckPending events in flight.
func (s *Service) PartitionWork(b *bus.Bus) func(context.Context, int, uint64) {
	return func(ctx context.Context, partition int, _ uint64) {
		durable := fmt.Sprintf("fulltext-%d", partition)
		subject := fmt.Sprintf("eb.evt.%d.>", partition)
		for ctx.Err() == nil {
			stop, err := b.ConsumePartition(ctx, bus.StreamEvents, durable, subject, consumerAckPending, func(hctx context.Context, m bus.Message) error {
				var ev events.Event
				if err := json.Unmarshal(m.Data, &ev); err != nil || ev.ID == "" {
					if err == nil {
						err = fmt.Errorf("fulltext: malformed event envelope")
					}
					s.log().ErrorContext(hctx, "discarding malformed event message", "subject", m.Subject, "err", err)
					return bus.DiscardMessage(err)
				}
				return s.classify(hctx, ev, s.HandleEvent(hctx, ev), m.NumDelivered)
			})
			if err == nil {
				<-ctx.Done()
				stop()
				return
			}
			s.log().ErrorContext(ctx, "full-text consumer start failed", "partition", partition, "err", err)
			t := time.NewTimer(time.Second)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
}
