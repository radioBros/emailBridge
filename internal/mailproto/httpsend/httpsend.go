// Package httpsend translates the result of a send through an HTTP API (Gmail API, Microsoft Graph, API
// gateways) into the SMTP-shaped stage error the submit retry policy classifies (S6 §6.1), so one policy
// decides retries for every transport.
package httpsend

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// StatusError is an HTTP send API's refusal: the server answered with Status.
type StatusError struct {
	Provider string
	Status   int
	Body     string // excerpt, never credentials
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%s API HTTP %d: %s", e.Provider, e.Status, e.Body)
}

// Classify maps err from an HTTP send into a *smtpc.StageError:
//
//   - 401/403 or a revoked token → AUTH stage, 535 (auth kind: the account needs new credentials);
//   - 429 → 451 4.7.1 rate limited;
//   - 408, 5xx → 451 temporary (the API refused before accepting);
//   - other 4xx → 554 permanent (the request itself is wrong; retrying will not change it);
//   - no response at all: a failure to resolve or connect is a temporary CONNECT failure, but anything after
//     the request may have reached the server (timeout, reset) is DATA-END without a reply — outcome unknown,
//     so `atMostOnce` never retries it and `standard` retries knowing it may duplicate.
//
// Errors that are neither (validation, configuration) are returned unchanged.
func Classify(err error) error {
	if err == nil {
		return nil
	}
	var se *smtpc.StageError
	if errors.As(err, &se) {
		return err
	}
	var st *StatusError
	if errors.As(err, &st) {
		return fromStatus(st, err)
	}
	if errs.CodeOf(err) == errs.CodeOauthRevoked {
		return &smtpc.StageError{Stage: smtpc.StageAuth, Code: 535, Enhanced: "5.7.8", Message: "access token rejected", Err: err}
	}
	if beforeConnect(err) {
		return &smtpc.StageError{Stage: smtpc.StageConnect, Err: err}
	}
	if transport(err) {
		return &smtpc.StageError{Stage: smtpc.StageDataEnd, Err: err}
	}
	return err
}

func fromStatus(st *StatusError, err error) error {
	msg := st.Body
	switch {
	case st.Status == http.StatusUnauthorized || st.Status == http.StatusForbidden:
		return &smtpc.StageError{Stage: smtpc.StageAuth, Code: 535, Enhanced: "5.7.8", Message: msg, Err: err}
	case st.Status == http.StatusTooManyRequests:
		return &smtpc.StageError{Stage: smtpc.StageMailFrom, Code: 451, Enhanced: "4.7.1", Message: "rate limited: " + msg, Err: err}
	case st.Status == http.StatusRequestTimeout || st.Status >= 500:
		return &smtpc.StageError{Stage: smtpc.StageMailFrom, Code: 451, Enhanced: "4.3.0", Message: msg, Err: err}
	case st.Status >= 400:
		return &smtpc.StageError{Stage: smtpc.StageDataEnd, Code: 554, Enhanced: "5.0.0", Message: msg, Err: err}
	}
	return err
}

// beforeConnect reports a failure that happened before any byte of the request left: DNS or dial.
func beforeConnect(err error) bool {
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return true
	}
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// transport reports a failure without an HTTP response: the request may or may not have been processed.
func transport(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) // *url.Error and net errors, also when wrapped by errs.Wrap
}
