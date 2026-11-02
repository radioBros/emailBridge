package sysmail

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

// HandleJob renders and sends one sysmail payload. Temporary SMTP failures are returned so the
// bus consumer can Nak/retry; permanent/auth/unknown and policy drops are swallowed (acked).
// In account mode the job is acked once submitted to the send pipeline (retries live there).
// With records (W72 §5) the job's record is claimed first: a cancelled or finished record is acked
// without sending, and the outcome is written back.
func (s *Service) HandleJob(ctx context.Context, data []byte) error {
	var j job
	if err := json.Unmarshal(data, &j); err != nil {
		s.log.Error("system mail bad payload", "err", err)
		return nil
	}
	tracked, send := s.claimRecord(ctx, j)
	if !send {
		return nil
	}
	state, err := s.process(ctx, j)
	if tracked {
		if rerr := s.Records.finish(ctx, j.ID, state, err != nil); rerr != nil {
			s.log.Warn("system mail record not updated", "err", rerr, "jobId", j.ID)
		}
	}
	return err
}

// claimRecord claims the job's record. It reports whether the record is tracked (its outcome must be
// written back) and whether to send at all. A database error fails open: the mail is sent untracked.
func (s *Service) claimRecord(ctx context.Context, j job) (tracked, send bool) {
	if s.Records == nil || j.ID == "" {
		return false, true
	}
	outcome, err := s.Records.claim(ctx, j.ID)
	switch {
	case err != nil:
		s.log.Warn("system mail record claim failed; sending untracked", "err", err, "jobId", j.ID)
		return false, true
	case outcome == claimSkip:
		s.log.Info("system mail cancelled or already handled; acked without sending", "kind", j.Kind, "jobId", j.ID)
		return false, false
	case outcome == claimMissed:
		return false, true
	}
	return true, true
}

// process renders and sends; it returns the record state of the outcome and the error to nak with
// (temporary failures only).
func (s *Service) process(ctx context.Context, j job) (string, error) {
	if !allowedRecipient(s.o.Env, j.To, s.o.AllowList) {
		s.log.Info("system mail dropped by recipient policy", logging.Address("to", j.To), "kind", j.Kind, "jobId", j.ID)
		return RecordDropped, nil
	}
	if !s.rate.allow(j.To, time.Now()) {
		// Ack and drop: retrying the same job would hit the same per-node bucket (P0 in-memory limit).
		s.log.Warn("system mail rate limited", logging.Address("to", j.To), "kind", j.Kind, "jobId", j.ID)
		return RecordDropped, nil
	}
	from := s.o.From
	if from == "" {
		from = "EmailBridge <noreply@localhost>"
	}
	r, err := Render(j.Kind, j.Locale, from, j.To, j.Data)
	if err != nil {
		s.log.Error("system mail render failed", "err", err, "kind", j.Kind, "jobId", j.ID)
		return RecordFailed, nil
	}
	if strings.EqualFold(strings.TrimSpace(s.o.Mode), ModeAccount) {
		return s.sendViaAccount(ctx, j, from, r.Raw)
	}
	send := s.send
	if send == nil {
		s.log.Error("system mail sender not configured", "kind", j.Kind, "jobId", j.ID)
		return RecordFailed, nil
	}
	err = send(ctx, s.o.SMTP, extractAddr(from), []string{j.To}, r.Raw)
	if err != nil {
		code := errs.CodeOf(err)
		s.log.Error("system mail send failed", "err", err, "code", string(code),
			logging.Address("to", j.To), "kind", j.Kind, "jobId", j.ID)
		if errs.Retryable(err) {
			return RecordPending, err
		}
		return RecordFailed, nil
	}
	s.log.Info("system mail sent", logging.Address("to", j.To), "kind", j.Kind, "jobId", j.ID)
	return RecordSent, nil
}

func (s *Service) sendViaAccount(ctx context.Context, j job, from string, raw []byte) (string, error) {
	sender := s.accountSender()
	if !s.accountEnabled() || sender == nil {
		s.log.Error("system mail account sender unavailable", logging.Address("to", j.To), "kind", j.Kind, "jobId", j.ID)
		return RecordFailed, nil
	}
	outboundID, err := sender.SendSystem(ctx, s.o.AccountID, from, j.To, raw)
	if err != nil {
		code := errs.CodeOf(err)
		s.log.Error("system mail account submit failed", "err", err, "code", string(code),
			logging.Address("to", j.To), "kind", j.Kind, "jobId", j.ID, "accountId", s.o.AccountID)
		if errs.Retryable(err) {
			return RecordPending, err
		}
		return RecordFailed, nil
	}
	s.log.Info("system mail submitted via account", logging.Address("to", j.To), "kind", j.Kind,
		"jobId", j.ID, "outboundId", outboundID, "accountId", s.o.AccountID)
	return RecordSent, nil
}

// StartWorker consumes EMAILBRIDGE_SYSMAIL on b until stop is called.
func (s *Service) StartWorker(ctx context.Context, b *bus.Bus) (stop func(), err error) {
	return b.ConsumeSysmail(ctx, s.HandleJob)
}
