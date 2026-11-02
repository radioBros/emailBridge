package policy

import (
	"errors"
	"io"
	"math"
	"net"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// stages are every stage of S6 §3.3, in order.
var stages = []string{
	string(smtpc.StageConnect), string(smtpc.StageEHLO), string(smtpc.StageAuth),
	string(smtpc.StageMailFrom), string(smtpc.StageRcptTo), string(smtpc.StageData),
	string(smtpc.StageDataEnd),
}

func afterBody(stage string) bool {
	return stage == string(smtpc.StageData) || stage == string(smtpc.StageDataEnd)
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }
func (timeoutErr) Temporary() bool {
	return true
}

var _ net.Error = timeoutErr{}

// TestClassifyEveryRowAtEveryStage walks the S6 §6.1 table: every row is classified at every stage.
func TestClassifyEveryRowAtEveryStage(t *testing.T) {
	boom := errors.New("connection refused")
	rows := []struct {
		row   string
		reply Reply
		err   error
		// want returns the expected kind and code for a stage.
		want func(stage string) (errs.Kind, errs.Code)
	}{
		{
			row: "2xx accepted",
			err: nil,
			want: func(string) (errs.Kind, errs.Code) {
				return KindSuccess, ""
			},
		},
		{
			row:   "421 service not available",
			reply: Reply{Code: 421, Text: "service not available"},
			err:   errors.New("421"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "450 mailbox unavailable",
			reply: Reply{Code: 450, Enhanced: "4.2.1", Text: "mailbox unavailable"},
			err:   errors.New("450"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "451 local error",
			reply: Reply{Code: 451, Enhanced: "4.3.0", Text: "local error"},
			err:   errors.New("451"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "452 insufficient storage",
			reply: Reply{Code: 452, Enhanced: "4.3.1", Text: "disk full"},
			err:   errors.New("452"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "454 TLS not available",
			reply: Reply{Code: 454, Enhanced: "4.3.3", Text: "tls unavailable"},
			err:   errors.New("454"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "other 4xx",
			reply: Reply{Code: 471, Text: "please retry"},
			err:   errors.New("471"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "4xx with enhanced 4.7.x",
			reply: Reply{Code: 451, Enhanced: "4.7.28", Text: "unusual traffic"},
			err:   errors.New("4.7.28"),
			want:  constant(errs.KindRateLimited, errs.CodeSMTPRateLimited),
		},
		{
			row:   "4xx matching a throttle pattern",
			reply: Reply{Code: 452, Enhanced: "4.5.3", Text: "Too many recipients, slow down"},
			err:   errors.New("throttled"),
			want:  constant(errs.KindRateLimited, errs.CodeSMTPRateLimited),
		},
		{
			row:   "503 bad sequence",
			reply: Reply{Code: 503, Enhanced: "5.5.1", Text: "bad sequence of commands"},
			err:   errors.New("503"),
			want:  constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:   "530 authentication required",
			reply: Reply{Code: 530, Enhanced: "5.7.0", Text: "authentication required"},
			err:   errors.New("530"),
			want:  authAtAuthStage,
		},
		{
			row:   "534 mechanism too weak",
			reply: Reply{Code: 534, Enhanced: "5.7.9", Text: "mechanism too weak"},
			err:   errors.New("534"),
			want:  authAtAuthStage,
		},
		{
			row:   "535 bad credentials",
			reply: Reply{Code: 535, Enhanced: "5.7.8", Text: "bad credentials"},
			err:   errors.New("535"),
			want:  authAtAuthStage,
		},
		{
			row:   "538 encryption required",
			reply: Reply{Code: 538, Enhanced: "5.7.11", Text: "encryption required"},
			err:   errors.New("538"),
			want:  authAtAuthStage,
		},
		{
			row:   "550 no such user",
			reply: Reply{Code: 550, Enhanced: "5.1.1", Text: "no such user"},
			err:   errors.New("550"),
			want:  constant(errs.KindPermanent, errs.CodeSMTPPermanent),
		},
		{
			row:   "554 rejected",
			reply: Reply{Code: 554, Enhanced: "5.7.1", Text: "rejected"},
			err:   errors.New("554"),
			want:  constant(errs.KindPermanent, errs.CodeSMTPPermanent),
		},
		{
			row:   "500 syntax error",
			reply: Reply{Code: 500, Text: "syntax error"},
			err:   errors.New("500"),
			want:  constant(errs.KindPermanent, errs.CodeSMTPPermanent),
		},
		{
			row:   "502 not implemented",
			reply: Reply{Code: 502, Text: "not implemented"},
			err:   errors.New("502"),
			want:  constant(errs.KindPermanent, errs.CodeSMTPPermanent),
		},
		{
			row:   "504 parameter not implemented",
			reply: Reply{Code: 504, Text: "parameter not implemented"},
			err:   errors.New("504"),
			want:  constant(errs.KindPermanent, errs.CodeSMTPPermanent),
		},
		{
			row:  "dial error / connection refused",
			err:  boom,
			want: temporaryUntilBody,
		},
		{
			row:  "timeout",
			err:  timeoutErr{},
			want: temporaryUntilBody,
		},
		{
			row:  "connection lost (EOF)",
			err:  io.EOF,
			want: temporaryUntilBody,
		},
		{
			row:  "TLS handshake failure",
			err:  errs.New(errs.CodeMailTLSFailed, "The server does not offer STARTTLS."),
			want: constant(errs.KindTemporary, errs.CodeSMTPTemporary),
		},
		{
			row:  "server SIZE below the message",
			err:  errs.New(errs.CodeMessageTooLarge, "too large"),
			want: constant(errs.KindPermanent, errs.CodeMessageTooLarge),
		},
		{
			row:  "SMTPUTF8 unsupported",
			err:  errs.New(errs.CodeSMTPPermanent, "needs SMTPUTF8"),
			want: constant(errs.KindPermanent, errs.CodeSMTPPermanent),
		},
		{
			row:  "no supported auth mechanism",
			err:  errs.New(errs.CodeSMTPAuthFailed, "no mechanism"),
			want: constant(errs.KindAuth, errs.CodeSMTPAuthFailed),
		},
		{
			// smtpc gives every unclassified failure at the auth stage the auth code; a transport
			// failure (no reply code) stays temporary, and is unknown once the body is on the wire.
			row:  "connection broke during a stage smtpc calls auth",
			err:  smtpc.Classify(string(smtpc.StageAuth), &smtpc.StageError{Stage: smtpc.StageAuth, Err: io.ErrUnexpectedEOF}),
			want: temporaryUntilBody,
		},
		{
			row:  "no recipients",
			err:  errs.New(errs.CodeNoRecipients, "none"),
			want: constant(errs.KindPermanent, errs.CodeNoRecipients),
		},
		{
			row:  "unparsable address",
			err:  errs.New(errs.CodeInvalidAddress, "bad"),
			want: constant(errs.KindPermanent, errs.CodeInvalidAddress),
		},
		{
			row:  "validation failure",
			err:  errs.New(errs.CodeValidationFailed, "bad"),
			want: constant(errs.KindPermanent, errs.CodeValidationFailed),
		},
		{
			row:  "capability missing",
			err:  errs.New(errs.CodeMailCapabilityMissing, "missing"),
			want: constant(errs.KindPermanent, errs.CodeMailCapabilityMissing),
		},
	}
	for _, r := range rows {
		for _, stage := range stages {
			wantKind, wantCode := r.want(stage)
			kind, code := Classify(stage, r.reply, r.err)
			if kind != wantKind || code != wantCode {
				t.Errorf("%s at %s: got %s/%s, want %s/%s", r.row, stage, kind, code, wantKind, wantCode)
			}
		}
	}
}

func constant(kind errs.Kind, code errs.Code) func(string) (errs.Kind, errs.Code) {
	return func(string) (errs.Kind, errs.Code) { return kind, code }
}

// authAtAuthStage: the auth replies of S6 §6.1 carry the auth kind at the auth stage; the same code
// anywhere else is just another 5xx, so it is permanent.
func authAtAuthStage(stage string) (errs.Kind, errs.Code) {
	if stage == string(smtpc.StageAuth) {
		return errs.KindAuth, errs.CodeSMTPAuthFailed
	}
	return errs.KindPermanent, errs.CodeSMTPPermanent
}

// temporaryUntilBody: a network failure is temporary until the body is on the wire, after which the
// outcome is unknown.
func temporaryUntilBody(stage string) (errs.Kind, errs.Code) {
	if afterBody(stage) {
		return errs.KindOutcomeUnknown, errs.CodeSMTPOutcomeUnknown
	}
	return errs.KindTemporary, errs.CodeSMTPTemporary
}

func TestClassify2xxWithAnError(t *testing.T) {
	kind, code := Classify(string(smtpc.StageDataEnd), Reply{Code: 250, Text: "ok"}, errors.New("surprise"))
	if kind != errs.KindTemporary || code != errs.CodeSMTPTemporary {
		t.Fatalf("got %s/%s", kind, code)
	}
}

func TestThrottled(t *testing.T) {
	for _, r := range []Reply{
		{Enhanced: "4.7.1"},
		{Text: "Rate limit exceeded"},
		{Text: "please SLOW DOWN"},
		{Text: "sending quota reached"},
	} {
		if !Throttled(r) {
			t.Errorf("%+v must be a throttle", r)
		}
	}
	for _, r := range []Reply{
		{Enhanced: "4.2.1", Text: "mailbox busy"},
		{Enhanced: "5.7.1", Text: "rejected"},
		{},
	} {
		if Throttled(r) {
			t.Errorf("%+v must not be a throttle", r)
		}
	}
}

func TestStageAndReply(t *testing.T) {
	wrapped := smtpc.Classify(string(smtpc.StageRcptTo), &smtpc.StageError{
		Stage: smtpc.StageRcptTo, Code: 450, Enhanced: "4.2.1", Message: "try later",
	})
	stage, reply := StageAndReply(wrapped)
	if stage != string(smtpc.StageRcptTo) || reply.Code != 450 || reply.Enhanced != "4.2.1" || reply.Text != "try later" {
		t.Fatalf("got %s %+v", stage, reply)
	}
	stage, reply = StageAndReply(errors.New("no stage"))
	if stage != string(smtpc.StageConnect) || reply != (Reply{}) {
		t.Fatalf("got %s %+v", stage, reply)
	}
}

// TestApplyMatrix is the S6 §6.2 matrix: kind × policy × attempt.
func TestApplyMatrix(t *testing.T) {
	settings := func(policy string) Settings {
		return Settings{Policy: policy, MaxAttempts: 3, BackoffBase: time.Second}
	}
	kinds := []errs.Kind{errs.KindTemporary, errs.KindRateLimited, errs.KindAuth, errs.KindPermanent, errs.KindOutcomeUnknown}
	for _, policy := range []string{Standard, AtMostOnce} {
		for _, kind := range kinds {
			for attempt := 1; attempt <= 4; attempt++ {
				got := Apply(settings(policy), kind, attempt, 0)
				want := Decision{}
				switch {
				case kind == errs.KindPermanent:
					// failed at every attempt under both policies
				case kind == errs.KindOutcomeUnknown && policy == AtMostOnce:
					want = Decision{OutcomeUnknown: true}
				case attempt < 3:
					want = Decision{Retry: true, Delay: got.Delay}
				}
				if got.Retry != want.Retry || got.OutcomeUnknown != want.OutcomeUnknown {
					t.Errorf("%s/%s attempt %d: got %+v, want %+v", policy, kind, attempt, got, want)
				}
				if got.Retry && got.Delay <= 0 {
					t.Errorf("%s/%s attempt %d: retry without a delay", policy, kind, attempt)
				}
			}
		}
	}
}

func TestApplyDefaults(t *testing.T) {
	// No policy, no maxAttempts, no base: standard with DefaultMaxAttempts and DefaultBackoffBase.
	got := Apply(Settings{}, errs.KindTemporary, DefaultMaxAttempts-1, 0)
	if !got.Retry || got.Delay <= 0 {
		t.Fatalf("one attempt left: %+v", got)
	}
	if got := Apply(Settings{}, errs.KindTemporary, DefaultMaxAttempts, 0); got.Retry {
		t.Fatalf("attempts exhausted: %+v", got)
	}
	if got := Apply(Settings{}, errs.KindOutcomeUnknown, 1, 0); !got.Retry {
		t.Fatalf("standard retries an unknown outcome: %+v", got)
	}
}

func TestBackoffBounds(t *testing.T) {
	s := Settings{BackoffBase: 5 * time.Second, MaxAttempts: 50}
	if d := Backoff(s, errs.KindTemporary, 1, 0); d != 5*time.Second {
		t.Fatalf("first attempt: %s", d)
	}
	if d := Backoff(s, errs.KindTemporary, 4, 0); d != 40*time.Second {
		t.Fatalf("fourth attempt: %s", d)
	}
	for _, attempt := range []int{0, 1, 7, 20, 30, 1 << 20} {
		d := Backoff(s, errs.KindTemporary, attempt, 0)
		if d < time.Second || d > MaxBackoff {
			t.Fatalf("attempt %d out of bounds: %s", attempt, d)
		}
	}
	// Jitter stays inside ±20 % and never leaves the bounds.
	jittered := Settings{BackoffBase: time.Minute, BackoffJitter: 0.2, Rand: func() float64 { return 1 }}
	if d := Backoff(jittered, errs.KindTemporary, 1, 0); d != 72*time.Second {
		t.Fatalf("upper jitter: %s", d)
	}
	jittered.Rand = func() float64 { return 0 }
	if d := Backoff(jittered, errs.KindTemporary, 1, 0); d != 48*time.Second {
		t.Fatalf("lower jitter: %s", d)
	}
	// A base that overflows the shift still produces a capped, positive delay.
	if d := Backoff(Settings{BackoffBase: time.Duration(math.MaxInt64 / 2)}, errs.KindTemporary, 3, 0); d != MaxBackoff {
		t.Fatalf("overflowing base: %s", d)
	}
	// Floors per kind.
	if d := Backoff(s, errs.KindRateLimited, 1, 0); d != MinRateLimited {
		t.Fatalf("throttle floor: %s", d)
	}
	if d := Backoff(s, errs.KindRateLimited, 1, 20*time.Minute); d != 20*time.Minute {
		t.Fatalf("provider hint: %s", d)
	}
	if d := Backoff(s, errs.KindAuth, 1, 0); d != MinAuth {
		t.Fatalf("auth floor: %s", d)
	}
	if d := Backoff(Settings{BackoffBase: 2 * time.Hour}, errs.KindAuth, 1, 0); d != MaxBackoff {
		t.Fatalf("cap applies before the floor: %s", d)
	}
}
