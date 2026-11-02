package imapc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"

	"github.com/emersion/go-imap/v2"

	"github.com/radiobros/emailbridge/internal/errs"
)

// ErrDead means the connection stopped responding (DONE timeout, deadline) and was closed.
var ErrDead = errors.New("imapc: connection not responding")

// classify maps a go-imap/net error to our error kinds and codes (S5 §7). op names the command.
func classify(op string, err error) error {
	if err == nil {
		return nil
	}
	var e *errs.E
	if errors.As(err, &e) {
		return err
	}
	var ie *imap.Error
	if errors.As(err, &ie) {
		switch ie.Code {
		case imap.ResponseCodeAuthenticationFailed, imap.ResponseCodeAuthorizationFailed, imap.ResponseCodeExpired, imap.ResponseCodePrivacyRequired:
			return errs.Wrap(err, errs.CodeMailAuthFailed, op)
		case imap.ResponseCodeUnavailable:
			return errs.Wrap(err, errs.CodeMailConnectFailed, op)
		case imap.ResponseCodeLimit:
			w := errs.Wrap(err, errs.CodeMailConnectFailed, op)
			w.Kind = errs.KindRateLimited
			return w
		}
		if op == "login" || op == "authenticate" {
			return errs.Wrap(err, errs.CodeMailAuthFailed, op)
		}
		return errs.Wrap(err, errs.CodeMailProtocolError, op)
	}
	var cert *tls.CertificateVerificationError
	var unk x509.UnknownAuthorityError
	var host x509.HostnameError
	var inv x509.CertificateInvalidError
	var rec tls.RecordHeaderError
	switch {
	case errors.As(err, &cert), errors.As(err, &unk), errors.As(err, &host), errors.As(err, &inv), errors.As(err, &rec),
		strings.Contains(err.Error(), "tls:"):
		return errs.Wrap(err, errs.CodeMailTLSFailed, op)
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, ErrDead):
		return errs.Wrap(err, errs.CodeMailTimeout, op)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return errs.Wrap(err, errs.CodeMailTimeout, op)
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.As(err, new(*net.OpError)) {
		return errs.Wrap(err, errs.CodeMailConnectFailed, op)
	}
	return errs.Wrap(err, errs.CodeMailProtocolError, op)
}
