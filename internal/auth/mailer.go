package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// Mail kinds the auth layer asks the system-mail queue to send (S10 §7).
const (
	MailMFACode       = "mfa_code"       // Data: code
	MailPasswordReset = "password_reset" // Data: token
)

// Mail is one system mail job. The queue (internal/sysmail, P0.18) renders the template for Kind in Locale
// (empty = the user's or the instance default) with Data.
type Mail struct {
	Kind   string
	To     string
	Locale string
	Data   map[string]string
}

// Mailer enqueues system mail. Limen's mail callbacks return no error, so they never send directly: they
// enqueue here, and failures are logged (S10 §2.4).
type Mailer interface {
	Enqueue(ctx context.Context, m Mail) error
}

// NoopMailer drops every mail with a warning; it is the default until system mail is configured.
type NoopMailer struct{ Log *slog.Logger }

// Enqueue implements Mailer.
func (n NoopMailer) Enqueue(ctx context.Context, m Mail) error {
	log := n.Log
	if log == nil {
		log = slog.Default()
	}
	log.WarnContext(ctx, "system mail disabled", "kind", m.Kind, logging.Address("to", m.To))
	return nil
}

// mailTimeout bounds an enqueue started from a callback that has no request context.
const mailTimeout = 10 * time.Second

func (s *Service) enqueue(ctx context.Context, m Mail) {
	if err := s.mailer.Enqueue(ctx, m); err != nil {
		s.log.ErrorContext(ctx, "system mail enqueue failed", "kind", m.Kind, logging.Address("to", m.To), "err", err)
		return
	}
	s.log.InfoContext(ctx, "system mail enqueued", "kind", m.Kind, logging.Address("to", m.To))
}

// sendOTP is the two-factor plugin's OTP callback (email codes).
func (s *Service) sendOTP(ctx context.Context, email, code string) {
	s.enqueue(ctx, Mail{Kind: MailMFACode, To: email, Data: map[string]string{"code": code}})
}

// sendPasswordReset is the credential-password plugin's reset callback (it has no context).
func (s *Service) sendPasswordReset(email, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), mailTimeout)
	defer cancel()
	s.enqueue(ctx, Mail{Kind: MailPasswordReset, To: email, Data: map[string]string{"token": token}})
}
