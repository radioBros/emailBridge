package errs

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// E is the EmailBridge typed error (S2 §5.1).
type E struct {
	Kind Kind
	Code Code
	Op   string
	Msg  string
	Err  error
	Safe map[string]any

	// 429 response hints (retry.go): Retry-After, its suppression, and the RateLimit-* state.
	retryAfter   time.Duration
	noRetryAfter bool
	rate         *RateInfo
}

// Error implements error.
func (e *E) Error() string {
	if e == nil {
		return ""
	}
	switch {
	case e.Op != "" && e.Msg != "" && e.Err != nil:
		return fmt.Sprintf("%s: %s: %v", e.Op, e.Msg, e.Err)
	case e.Op != "" && e.Msg != "":
		return fmt.Sprintf("%s: %s", e.Op, e.Msg)
	case e.Op != "" && e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Op, e.Err)
	case e.Msg != "" && e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Msg, e.Err)
	case e.Msg != "":
		return e.Msg
	case e.Err != nil:
		return e.Err.Error()
	case e.Code != "":
		return string(e.Code)
	default:
		return "error"
	}
}

// Unwrap returns the wrapped error.
func (e *E) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// New builds an E whose Kind is taken from the catalogue for code.
func New(code Code, msg string) *E {
	info, ok := catalog[code]
	kind := KindInternal
	if ok {
		kind = info.Kind
	}
	return &E{Kind: kind, Code: code, Msg: msg}
}

// Wrap wraps err with a catalogue code and operation name.
func Wrap(err error, code Code, op string) *E {
	info, ok := catalog[code]
	kind := KindInternal
	if ok {
		kind = info.Kind
	}
	return &E{Kind: kind, Code: code, Op: op, Err: err}
}

// WithSafe attaches a safe-to-log key/value and returns the same E.
func (e *E) WithSafe(k string, v any) *E {
	if e.Safe == nil {
		e.Safe = map[string]any{}
	}
	e.Safe[k] = v
	return e
}

// KindOf walks the error chain and returns the Kind. Unknown errors map to
// KindInternal. An explicit *E wins; otherwise context.DeadlineExceeded → temporary/timeout;
// context.Canceled → temporary.
func KindOf(err error) Kind {
	if err == nil {
		return KindInternal
	}
	var e *E
	if errors.As(err, &e) && e.Kind != "" {
		return e.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return KindTemporary
	}
	return KindInternal
}

// CodeOf walks the error chain and returns the Code. context.DeadlineExceeded
// maps to CodeTimeout; unknown errors map to CodeInternalError.
func CodeOf(err error) Code {
	if err == nil {
		return CodeInternalError
	}
	var e *E
	if errors.As(err, &e) && e.Code != "" {
		return e.Code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return CodeTimeout
	}
	return CodeInternalError
}

// IsKind reports whether err has the given kind.
func IsKind(err error, kind Kind) bool {
	return KindOf(err) == kind
}

// Retryable reports whether the error should be retried (temporary, rate_limited).
func Retryable(err error) bool {
	k := KindOf(err)
	return k == KindTemporary || k == KindRateLimited
}

// Reportable reports whether the error should be sent to Sentry (S12 §12):
// internal and config are reported; not_found, conflict, rate_limited,
// validation, auth and forbidden are never reported.
func Reportable(err error) bool {
	switch KindOf(err) {
	case KindInternal, KindConfig:
		return true
	default:
		return false
	}
}

// Lookup returns catalogue info for a code.
func Lookup(code Code) (kind Kind, status int, title string, ok bool) {
	info, ok := catalog[code]
	if !ok {
		return "", 0, "", false
	}
	return info.Kind, info.Status, info.Title, true
}
