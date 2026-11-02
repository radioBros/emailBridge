package httpsend

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/submit/policy"
)

func TestClassifyFeedsTheRetryPolicy(t *testing.T) {
	status := func(n int) error {
		return errs.Wrap(&StatusError{Provider: "Test", Status: n, Body: "x"}, errs.CodeServiceUnavailable, "test.status")
	}
	dial := &url.Error{Op: "Post", URL: "https://api.example.test", Err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}}
	dns := &url.Error{Op: "Post", URL: "https://api.example.test", Err: &net.DNSError{Err: "no such host", Name: "api.example.test"}}
	reset := errs.Wrap(&url.Error{Op: "Post", URL: "https://api.example.test", Err: &net.OpError{Op: "read", Net: "tcp", Err: errors.New("reset")}},
		errs.CodeServiceUnavailable, "test.http")
	for _, tc := range []struct {
		name string
		err  error
		want errs.Kind
	}{
		{"401", status(401), errs.KindAuth},
		{"403", status(403), errs.KindAuth},
		{"revoked token", errs.New(errs.CodeOauthRevoked, "revoked"), errs.KindAuth},
		{"429", status(429), errs.KindRateLimited},
		{"500", status(500), errs.KindTemporary},
		{"408", status(408), errs.KindTemporary},
		{"400", status(400), errs.KindPermanent},
		{"422", status(422), errs.KindPermanent},
		{"dial", dial, errs.KindTemporary},
		{"dns", dns, errs.KindTemporary},
		{"reset after send", reset, errs.KindOutcomeUnknown},
		{"timeout", context.DeadlineExceeded, errs.KindOutcomeUnknown},
		{"validation stays permanent", errs.New(errs.CodeValidationFailed, "empty"), errs.KindPermanent},
	} {
		c := Classify(tc.err)
		stage, reply := policy.StageAndReply(c)
		if kind, _ := policy.Classify(stage, reply, c); kind != tc.want {
			t.Errorf("%s: kind %s, want %s", tc.name, kind, tc.want)
		}
	}
	if Classify(nil) != nil {
		t.Fatal("nil")
	}
	se := &smtpc.StageError{Stage: smtpc.StageRcptTo, Code: 550}
	if got := Classify(se); !errors.Is(got, se) {
		t.Fatal("stage errors pass through")
	}
	if got := (&StatusError{Provider: "P", Status: 418, Body: "b"}).Error(); got != "P API HTTP 418: b" {
		t.Fatal(got)
	}
	if c := Classify(status(302)); !errors.Is(c, c) || errs.CodeOf(c) != errs.CodeServiceUnavailable {
		t.Fatalf("non-error status passes through: %v", c)
	}
}
