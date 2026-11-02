// Package smtpc is EmailBridge's SMTP client layer over go-smtp (the only package importing it): Session
// (session.go) owns the command sequence, stages, timeouts and the protocol tap; SendSimple is the one-shot
// sender used by system mail.
package smtpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
)

// TLSMode selects how the client secures the SMTP connection (S9 EB_SYSMAIL_SMTP_TLS).
type TLSMode string

// TLS modes.
const (
	TLSImplicit TLSMode = "implicit"
	TLSStartTLS TLSMode = "starttls"
	TLSNone     TLSMode = "none"
)

// Options configure SendSimple.
type Options struct {
	Host      string
	Port      int
	TLS       TLSMode
	User      string
	Pass      string
	EHLO      string // default "localhost"
	TLSConfig *tls.Config
	// Dialer overrides the connect dialer (tests). Timeout 30 s when nil.
	Dialer interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}
}

const (
	connectTimeout    = 30 * time.Second
	commandTimeout    = 60 * time.Second
	submissionTimeout = 10 * time.Minute
)

// SendSimple dials, greets, optionally authenticates, and sends one message (MAIL/RCPT/DATA) on a
// one-shot Session. Errors are classified into errs codes smtp_temporary, smtp_permanent,
// smtp_auth_failed, smtp_outcome_unknown, smtp_rate_limited.
func SendSimple(ctx context.Context, o Options, from string, to []string, raw []byte) error {
	if o.Host == "" {
		return errs.New(errs.CodeSMTPTemporary, "smtp host is required")
	}
	if o.Port <= 0 {
		o.Port = 465
	}
	if len(to) == 0 {
		return errs.New(errs.CodeSMTPPermanent, "at least one recipient is required")
	}
	so := SessionOptions{
		Addr:      net.JoinHostPort(o.Host, strconv.Itoa(o.Port)),
		TLS:       o.TLS,
		TLSConfig: o.TLSConfig,
		Username:  o.User,
		Password:  o.Pass,
		EHLO:      o.EHLO,
	}
	if o.Dialer != nil {
		so.Dial = o.Dialer.DialContext
	}
	s, err := Open(ctx, so)
	if err != nil {
		return err
	}
	defer func() { _ = s.Close() }()
	res, err := s.Send(ctx, Envelope{From: from, To: to, Size: int64(len(raw))}, bytes.NewReader(raw), Hooks{})
	if err != nil {
		return err
	}
	if len(res.Rejected) > 0 { // system mail goes to one recipient; any refusal is a failure
		r := res.Rejected[0]
		return errs.New(errs.CodeSMTPPermanent, "A recipient was refused.").WithSafe("code", r.Code).WithSafe("enhanced", r.Enhanced)
	}
	return nil
}

// Classify maps a go-smtp / network error at stage to an errs.E (S6 §6.1, SP2).
func Classify(stage string, err error) error {
	if err == nil {
		return nil
	}
	var se *smtp.SMTPError
	if errors.As(err, &se) {
		if se.EnhancedCode[0] == 4 && se.EnhancedCode[1] == 7 {
			return errs.Wrap(err, errs.CodeSMTPRateLimited, "smtpc."+stage)
		}
		switch se.Code {
		case 530, 534, 535, 538:
			return errs.Wrap(err, errs.CodeSMTPAuthFailed, "smtpc."+stage)
		case 503:
			return errs.Wrap(err, errs.CodeSMTPTemporary, "smtpc."+stage)
		}
		if se.Temporary() {
			return errs.Wrap(err, errs.CodeSMTPTemporary, "smtpc."+stage)
		}
		return errs.Wrap(err, errs.CodeSMTPPermanent, "smtpc."+stage)
	}
	if stage == string(StageData) || stage == string(StageDataEnd) {
		if errors.Is(err, io.EOF) || isTimeout(err) || isConnLost(err) {
			return errs.Wrap(err, errs.CodeSMTPOutcomeUnknown, "smtpc."+stage)
		}
	}
	// At the auth stage only a server reply (5xx above) means the credentials were refused; a dropped
	// connection or timeout during AUTH is a temporary network failure, not an auth failure (no 15-minute
	// auth backoff, no account.auth_failed).
	return errs.Wrap(err, errs.CodeSMTPTemporary, "smtpc."+stage)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func isConnLost(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "connection reset") || strings.Contains(s, "broken pipe") ||
		errors.Is(err, net.ErrClosed)
}
