package msa

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Backend is the go-smtp Backend for the MSA.
type Backend struct {
	Auth     *Authenticator
	Submit   Submitter
	MsgRate  *limiter
	MaxBytes int64
	MaxRcpt  int
	Log      *slog.Logger
	Gate     FeatureGate
}

// NewSession implements smtp.Backend.
func (b *Backend) NewSession(c *smtp.Conn) (smtp.Session, error) {
	if b.Gate != nil && !b.Gate.MSAEnabled(context.Background()) {
		return nil, &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 3, 2}, Message: "Service not available"}
	}
	remote := ""
	if c != nil && c.Conn() != nil {
		remote = c.Conn().RemoteAddr().String()
	}
	return &Session{b: b, remote: remote}, nil
}

// Session is one SMTP submission session.
type Session struct {
	b      *Backend
	remote string
	authed *Authed
	from   string
	rcpt   []string
}

var _ smtp.AuthSession = (*Session)(nil)

// AuthMechanisms implements smtp.AuthSession.
func (s *Session) AuthMechanisms() []string { return []string{sasl.Plain, sasl.Login} }

// Auth implements smtp.AuthSession.
func (s *Session) Auth(mech string) (sasl.Server, error) {
	switch mech {
	case sasl.Plain:
		return sasl.NewPlainServer(func(_, username, password string) error {
			return s.login(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{auth: func(username, password string) error {
			return s.login(username, password)
		}}, nil
	default:
		return nil, smtp.ErrAuthUnknownMechanism
	}
}

// loginServer is a minimal SASL LOGIN server (obsolete; still offered by many MSAs).
type loginServer struct {
	step int
	user string
	auth func(username, password string) error
}

func (a *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch a.step {
	case 0:
		if response == nil {
			a.step = 1
			return []byte("Username:"), false, nil
		}
		a.user = string(response)
		a.step = 2
		return []byte("Password:"), false, nil
	case 1:
		a.user = string(response)
		a.step = 2
		return []byte("Password:"), false, nil
	case 2:
		err = a.auth(a.user, string(response))
		return nil, true, err
	default:
		return nil, true, sasl.ErrUnexpectedClientResponse
	}
}

func (s *Session) login(username, password string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a, err := s.b.Auth.Authenticate(ctx, username, password, s.remote)
	if err != nil {
		return smtp.ErrAuthFailed
	}
	s.authed = a
	return nil
}

// Mail implements smtp.Session.
func (s *Session) Mail(from string, _ *smtp.MailOptions) error {
	if s.authed == nil {
		return smtp.ErrAuthRequired
	}
	s.from = from
	s.rcpt = nil
	return nil
}

// Rcpt implements smtp.Session.
func (s *Session) Rcpt(to string, _ *smtp.RcptOptions) error {
	if s.authed == nil {
		return smtp.ErrAuthRequired
	}
	if s.b.MaxRcpt > 0 && len(s.rcpt) >= s.b.MaxRcpt {
		return &smtp.SMTPError{Code: 452, EnhancedCode: smtp.EnhancedCode{4, 5, 3}, Message: "Too many recipients"}
	}
	s.rcpt = append(s.rcpt, to)
	return nil
}

// Data implements smtp.Session.
func (s *Session) Data(r io.Reader) error {
	if s.authed == nil {
		return smtp.ErrAuthRequired
	}
	if len(s.rcpt) == 0 {
		return &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 5, 1}, Message: "No recipients"}
	}
	limit := s.b.MaxBytes
	if limit <= 0 {
		limit = 25 << 20
	}
	raw, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > limit {
		return &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 3, 4}, Message: "Message too large"}
	}
	acc := s.authed.Account
	if s.b.MsgRate != nil && !s.b.MsgRate.allow(acc.ID) {
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Rate limit exceeded"}
	}
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), acc.OrganizationID), s.authed.Caller)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	// The SMTP envelope decides delivery, as for any submission server (P7.1 §2.3): RCPT TO recipients that
	// are not in the To / Cc headers are blind copies; an empty MAIL FROM falls back to the From header.
	res, err := s.b.Submit.Submit(ctx, acc.ID, submit.SubmitRequest{
		Raw:      base64.StdEncoding.EncodeToString(raw),
		Envelope: &submit.EnvelopeInput{From: s.from, To: append([]string(nil), s.rcpt...)},
	}, "", false)
	if err != nil {
		return mapSubmitErr(err)
	}
	if res == nil {
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure"}
	}
	return nil
}

// Reset implements smtp.Session.
func (s *Session) Reset() { s.from = ""; s.rcpt = nil }

// Logout implements smtp.Session.
func (s *Session) Logout() error { return nil }

func mapSubmitErr(err error) error {
	code := errs.CodeOf(err)
	switch code {
	case errs.CodeValidationFailed, errs.CodeRawAndFields, "message_too_large", "suppressed":
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Message rejected"}
	case errs.CodeRateLimited, "quota_exceeded":
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Rate limit exceeded"}
	case errs.CodeForbidden, errs.CodeFeatureDisabled, errs.CodeUnauthenticated, errs.CodeScopeMissing:
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 0}, Message: "Not allowed"}
	case errs.CodeNotFound:
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "Account unavailable"}
	default:
		if errors.Is(err, context.DeadlineExceeded) {
			return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 4, 2}, Message: "Temporary failure"}
		}
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "Temporary failure"}
	}
}
