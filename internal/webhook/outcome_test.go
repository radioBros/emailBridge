package webhook

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestClassifyResponseTable(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		status     int
		retry4xx   bool
		success    bool
		retry      bool
		disable    bool
		kind       string
		retryAfter string
		delay      time.Duration
	}{
		{200, false, true, false, false, "", "", 0},
		{299, false, true, false, false, "", "", 0},
		{301, false, false, false, false, string(errs.KindPermanent), "", 0},
		{400, true, false, true, false, string(errs.KindTemporary), "", 0},
		{408, false, false, true, false, string(errs.KindTemporary), "", 0},
		{410, true, false, false, true, string(errs.KindPermanent), "", 0},
		{425, false, false, true, false, string(errs.KindTemporary), "", 0},
		{429, false, false, true, false, string(errs.KindRateLimited), "12", 12 * time.Second},
		{503, false, false, true, false, string(errs.KindTemporary), "", 0},
	}
	for _, tc := range tests {
		headers := http.Header{}
		headers.Set("Retry-After", tc.retryAfter)
		got := classifyResponse(tc.status, tc.retry4xx, headers, now)
		if got.Success != tc.success || got.Retry != tc.retry || got.Disable != tc.disable ||
			got.Attempt.Kind != tc.kind || got.RetryAfter != tc.delay {
			t.Errorf("%d: %+v", tc.status, got)
		}
	}
}

func TestRetryAfterAndBackoffBounds(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if got := parseRetryAfter(now.Add(30*time.Second).Format(http.TimeFormat), now); got != 30*time.Second {
		t.Fatalf("http date = %v", got)
	}
	for _, value := range []string{"", "bad", "0", "-2", now.Add(-time.Second).Format(http.TimeFormat)} {
		if got := parseRetryAfter(value, now); got != 0 {
			t.Errorf("%q = %v", value, got)
		}
	}
	if parseRetryAfter("999999", now) != time.Hour {
		t.Fatal("retry-after cap")
	}
	if got := retryDelay(1, 5*time.Second, .2, 0); got != 4*time.Second {
		t.Fatalf("low jitter = %v", got)
	}
	if got := retryDelay(1, 5*time.Second, .2, 1); got != 6*time.Second {
		t.Fatalf("high jitter = %v", got)
	}
	if got := retryDelay(30, 5*time.Second, 2, 2); got != time.Hour {
		t.Fatalf("cap = %v", got)
	}
	if got := retryDelay(0, 0, -1, -1); got != 5*time.Second {
		t.Fatalf("defaults = %v", got)
	}
	if parseDuration("2s", time.Second) != 2*time.Second ||
		parseDuration("bad", time.Second) != time.Second ||
		parseDuration("-1s", time.Second) != time.Second {
		t.Fatal("duration parsing")
	}
}

func TestClassifyRequestErrors(t *testing.T) {
	tests := []struct {
		err   error
		retry bool
		code  errs.Code
	}{
		{fmtWrap(egress.ErrBlocked), false, errs.CodeEndpointURLBlocked},
		{context.DeadlineExceeded, true, errs.CodeWebhookTimeout},
		{x509.UnknownAuthorityError{}, true, errs.CodeWebhookTLSFailed},
		{errors.New("remote TLS alert"), true, errs.CodeWebhookTLSFailed},
		{errors.New("connection reset"), true, errs.CodeWebhookHTTPError},
	}
	for _, tc := range tests {
		got := classifyRequestError(tc.err)
		if got.Retry != tc.retry || got.Attempt.Error != string(tc.code) {
			t.Errorf("%v: %+v", tc.err, got)
		}
	}
	if statusError(503) != "HTTP 503 Service Unavailable" {
		t.Fatal(statusError(503))
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("wrapped"), err) }
