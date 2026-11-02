// Package policy turns the outcome of one SMTP attempt into a kind and a problem code, and applies the
// send policies of S6 §6. Both steps are pure functions of their inputs so the tables of S6 §6.1 and
// §6.2 can be tested exhaustively (PLAN.md Testing: 100 % coverage here).
package policy

import (
	"errors"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// KindSuccess is the kind of an accepted message; every other kind is an errs.Kind the error model
// already defines (temporary, rate_limited, auth, permanent, outcome_unknown).
const KindSuccess errs.Kind = "success"

// Send policies (S6 §6.2); they mirror accounts.Policy*.
const (
	Standard   = "standard"
	AtMostOnce = "atMostOnce"
)

// Retry bounds (S6 §6.2).
const (
	// MaxBackoff caps every computed delay.
	MaxBackoff = time.Hour
	// MinRateLimited is the floor for a throttled attempt without a provider hint.
	MinRateLimited = 5 * time.Minute
	// MinAuth is the floor for a rejected-credentials attempt.
	MinAuth = 15 * time.Minute
	// DefaultMaxAttempts and DefaultBackoffBase apply when neither the message, the SMTP
	// configuration nor EB_SUBMIT_* set a value.
	DefaultMaxAttempts = 10
	DefaultBackoffBase = 5 * time.Second
)

// Reply is the server's answer to the command that failed. Code 0 means the server never answered
// (network error, lost connection, timeout).
type Reply struct {
	Code     int
	Enhanced string
	Text     string
}

// authReplies are the auth-stage rejections of S6 §6.1.
var authReplies = map[int]bool{530: true, 534: true, 535: true, 538: true}

// throttlePatterns are the provider texts that mean "slow down" without an enhanced 4.7.x code. A new
// provider is added here and nowhere else (S6 §6.1).
var throttlePatterns = []string{
	"rate limit",
	"ratelimit",
	"throttl",
	"too many messages",
	"too many recipients",
	"too many connections",
	"concurrent connections",
	"message rate",
	"sending quota",
	"quota exceeded",
	"try again later",
	"slow down",
}

// Throttled reports whether a temporary reply is a throttle: an enhanced 4.7.x code or one of the
// provider patterns.
func Throttled(r Reply) bool {
	if strings.HasPrefix(r.Enhanced, "4.7.") {
		return true
	}
	text := strings.ToLower(r.Text)
	for _, p := range throttlePatterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// StageAndReply extracts the stage and the SMTP reply an smtpc error carries. Errors raised before any
// command (pool, options) report the connect stage and an empty reply.
func StageAndReply(err error) (string, Reply) {
	var se *smtpc.StageError
	if errors.As(err, &se) {
		return string(se.Stage), Reply{Code: se.Code, Enhanced: se.Enhanced, Text: se.Message}
	}
	return string(smtpc.StageConnect), Reply{}
}

// network reports whether err is a transport failure rather than a server verdict: smtpc gives network
// failures a stage but no reply code.
func network(err error) bool {
	var se *smtpc.StageError
	return errors.As(err, &se) && se.Code == 0
}

// Classify maps the result of an attempt at stage to a kind and a problem code (S6 §6.1). err == nil is
// the only success; everything else is classified from the reply when the server answered, and from the
// error and the stage when it did not.
func Classify(stage string, reply Reply, err error) (errs.Kind, errs.Code) {
	switch {
	case err == nil:
		return KindSuccess, ""
	case reply.Code > 0:
		return fromReply(stage, reply)
	default:
		return fromError(stage, err)
	}
}

func fromReply(stage string, r Reply) (errs.Kind, errs.Code) {
	switch {
	case r.Code < 400:
		// A 2xx/3xx together with an error is a protocol surprise, never an acceptance.
		return errs.KindTemporary, errs.CodeSMTPTemporary
	case r.Code < 500:
		if Throttled(r) {
			return errs.KindRateLimited, errs.CodeSMTPRateLimited
		}
		return errs.KindTemporary, errs.CodeSMTPTemporary
	case r.Code == 503:
		// PLAN rule "5xx permanent except 503": a bad command sequence is our fault and retriable.
		return errs.KindTemporary, errs.CodeSMTPTemporary
	case stage == string(smtpc.StageAuth) && authReplies[r.Code]:
		return errs.KindAuth, errs.CodeSMTPAuthFailed
	default:
		return errs.KindPermanent, errs.CodeSMTPPermanent
	}
}

func fromError(stage string, err error) (errs.Kind, errs.Code) {
	switch code := errs.CodeOf(err); code {
	case errs.CodeMessageTooLarge, errs.CodeNoRecipients, errs.CodeInvalidAddress,
		errs.CodeValidationFailed, errs.CodeMailCapabilityMissing:
		// Checked before the envelope is offered, so nothing was sent and nothing will change on retry.
		return errs.KindPermanent, code
	case errs.CodeSMTPPermanent, errs.CodePresendRejected:
		// A pre-send policy reject is a decision about this message: retrying gives the same answer.
		return errs.KindPermanent, code
	case errs.CodeSMTPAuthFailed:
		// Rejected credentials wait MinAuth; a connection that broke during AUTH is only temporary.
		if !network(err) {
			return errs.KindAuth, code
		}
	case errs.CodeMailTLSFailed:
		return errs.KindTemporary, errs.CodeSMTPTemporary
	case errs.CodeSMTPRateLimited:
		// API gateways report throttling as a code (no SMTP reply): back off like a 4.7.x reply.
		return errs.KindRateLimited, code
	}
	if stage == string(smtpc.StageData) || stage == string(smtpc.StageDataEnd) {
		// The body was on the wire and no final reply arrived: the server may have accepted it.
		return errs.KindOutcomeUnknown, errs.CodeSMTPOutcomeUnknown
	}
	return errs.KindTemporary, errs.CodeSMTPTemporary
}

// Settings are the resolved retry knobs of one message (message → SMTP configuration → EB_SUBMIT_*).
type Settings struct {
	Policy        string
	MaxAttempts   int
	BackoffBase   time.Duration
	BackoffJitter float64
	// Rand returns a value in [0, 1) for the jitter; nil means no jitter.
	Rand func() float64
}

func (s Settings) policy() string {
	if s.Policy == AtMostOnce {
		return AtMostOnce
	}
	return Standard
}

func (s Settings) attempts() int {
	if s.MaxAttempts > 0 {
		return s.MaxAttempts
	}
	return DefaultMaxAttempts
}

func (s Settings) base() time.Duration {
	if s.BackoffBase > 0 {
		return s.BackoffBase
	}
	return DefaultBackoffBase
}

// Decision is what the worker does after a failed attempt (S6 §6.2). Retry sends the message back to
// `queued` with `nextAttemptAt = now + Delay`; otherwise the message is final: `outcomeUnknown` when
// OutcomeUnknown is set, else `failed`.
type Decision struct {
	Retry          bool
	Delay          time.Duration
	OutcomeUnknown bool
}

// Apply decides what happens after the attempt-th failed attempt of kind (1 = the first attempt). hint
// is the provider's retry hint for throttles (0 when it gave none). It is never called for a success.
func Apply(s Settings, kind errs.Kind, attempt int, hint time.Duration) Decision {
	switch kind {
	case errs.KindPermanent:
		return Decision{}
	case errs.KindOutcomeUnknown:
		if s.policy() == AtMostOnce {
			// The message may be on its way: atMostOnce never sends it a second time.
			return Decision{OutcomeUnknown: true}
		}
	}
	if attempt >= s.attempts() {
		return Decision{}
	}
	return Decision{Retry: true, Delay: Backoff(s, kind, attempt, hint)}
}

// Backoff is backoffBase × 2^(attempt−1) ± backoffJitter, capped at MaxBackoff and then raised to the
// floor of the kind (throttles wait at least the provider hint or MinRateLimited, auth at least MinAuth).
func Backoff(s Settings, kind errs.Kind, attempt int, hint time.Duration) time.Duration {
	shift := attempt - 1
	switch {
	case shift < 0:
		shift = 0
	case shift > 20: // 2^20 × any sane base is already far past the cap
		shift = 20
	}
	d := s.base() << shift
	if d <= 0 {
		d = MaxBackoff
	}
	if s.BackoffJitter > 0 && s.Rand != nil {
		d += time.Duration(float64(d) * s.BackoffJitter * (2*s.Rand() - 1))
	}
	d = min(max(d, time.Second), MaxBackoff)
	switch kind {
	case errs.KindRateLimited:
		d = max(d, hint, MinRateLimited)
	case errs.KindAuth:
		d = max(d, MinAuth)
	}
	return d
}
