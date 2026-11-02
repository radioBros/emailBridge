package sysmail

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/store"
)

// Publisher publishes to the sysmail stream (narrowed for tests).
type Publisher interface {
	Publish(ctx context.Context, subject string, data []byte, msgID string, headers map[string]string) (duplicate bool, err error)
}

// Sender sends a rendered message (narrowed for tests).
type Sender func(ctx context.Context, o smtpc.Options, from string, to []string, raw []byte) error

// Options configure the system-mail service (from EB_SYSMAIL_* and process env).
type Options struct {
	Mode         string
	From         string
	Env          string // EB_ENV
	AllowList    []string
	SMTP         smtpc.Options
	AccountID    string // EB_SYSMAIL_ACCOUNT_ID when Mode is account
	Account      AccountSender
	CheckAccount AccountChecker
	Log          *slog.Logger
}

// Service enqueues system-mail jobs.
type Service struct {
	o         Options
	pub       Publisher
	log       *slog.Logger
	send      Sender
	rate      *rateLimiter
	accountOK int32 // atomic: 1 when mode=account is live
	mu        sync.Mutex
	// LocaleByEmail fills Mail.Locale when the caller left it empty (recipient's ui.locale).
	LocaleByEmail func(ctx context.Context, email string) string
	// Records keeps one sysmailJobs record per queued mail so the queue page can list and cancel it (W72 §5);
	// nil (no database) keeps the stream-only behaviour.
	Records *Records
}

// New builds a Service. pub may be a *bus.Bus.
func New(o Options, pub Publisher) *Service {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{o: o, pub: pub, log: log, send: smtpc.SendSimple, rate: newRateLimiter()}
}

// Enqueue publishes a system-mail job. Mode off logs a warning and drops; mode account without a
// validated AccountSender returns feature_disabled.
func (s *Service) Enqueue(ctx context.Context, m Mail) error {
	if s == nil {
		return errs.New(errs.CodeInternalError, "sysmail not configured")
	}
	mode := strings.ToLower(strings.TrimSpace(s.o.Mode))
	if mode == "" {
		mode = ModeOff
	}
	switch mode {
	case ModeOff:
		s.log.Warn("system mail dropped (mode=off)", logging.Address("to", m.To), "kind", m.Kind)
		return nil
	case ModeAccount:
		if !s.accountEnabled() {
			return errs.New(errs.CodeFeatureDisabled, "system mail through a connected account is not available")
		}
	case ModeSMTP:
		// ok
	default:
		return errs.New(errs.CodeValidationFailed, "unknown sysmail mode")
	}
	if strings.TrimSpace(m.To) == "" || !knownKinds[m.Kind] {
		return errs.New(errs.CodeValidationFailed, "system mail requires kind and to")
	}
	if strings.TrimSpace(m.Locale) == "" && s.LocaleByEmail != nil {
		m.Locale = s.LocaleByEmail(ctx, m.To)
	}
	m.Locale = NormalizeLocale(m.Locale)
	id := store.NewID(store.PrefixJob)
	j := job{ID: id, Kind: m.Kind, To: m.To, Locale: m.Locale, Data: m.Data}
	raw, err := json.Marshal(j)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "sysmail.enqueue")
	}
	if s.pub == nil {
		return errs.New(errs.CodeInternalError, "sysmail bus not configured")
	}
	if s.Records != nil {
		// Fail open: without its record the mail still sends (the worker treats a missing record as legacy).
		if rerr := s.Records.insert(ctx, j); rerr != nil {
			s.log.Warn("system mail record not stored; it cannot be cancelled from the queue page", "err", rerr, "jobId", id)
		}
	}
	_, err = s.pub.Publish(ctx, bus.SysmailSubject, raw, id, nil)
	if err != nil {
		if s.Records != nil {
			if rerr := s.Records.markFailed(ctx, id); rerr != nil {
				s.log.Warn("system mail record not updated", "err", rerr, "jobId", id)
			}
		}
		return errs.Wrap(err, errs.CodeInternalError, "sysmail.enqueue")
	}
	s.log.Info("system mail enqueued", logging.Address("to", m.To), "kind", m.Kind, "jobId", id)
	return nil
}
