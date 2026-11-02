package webhook

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

const maxBackoff = time.Hour

type attemptOutcome struct {
	Attempt    DeliveryAttempt
	Success    bool
	Retry      bool
	Disable    bool
	RetryAfter time.Duration
}

func classifyResponse(status int, retryOn4xx bool, headers http.Header, now time.Time) attemptOutcome {
	out := attemptOutcome{Attempt: DeliveryAttempt{StatusCode: status}}
	switch {
	case status >= 200 && status < 300:
		out.Success = true
	case status == http.StatusGone:
		out.Attempt.Kind = string(errs.KindPermanent)
		out.Attempt.Error = string(errs.CodeWebhookHTTPError)
		out.Disable = true
	case status == http.StatusTooManyRequests:
		out.Attempt.Kind = string(errs.KindRateLimited)
		out.Attempt.Error = string(errs.CodeWebhookHTTPError)
		out.Retry = true
		out.RetryAfter = parseRetryAfter(headers.Get("Retry-After"), now)
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly || status >= 500:
		out.Attempt.Kind = string(errs.KindTemporary)
		out.Attempt.Error = string(errs.CodeWebhookHTTPError)
		out.Retry = true
	case status >= 400 && status < 500 && retryOn4xx:
		out.Attempt.Kind = string(errs.KindTemporary)
		out.Attempt.Error = string(errs.CodeWebhookHTTPError)
		out.Retry = true
	default:
		out.Attempt.Kind = string(errs.KindPermanent)
		out.Attempt.Error = string(errs.CodeWebhookHTTPError)
	}
	return out
}

func classifyRequestError(err error) attemptOutcome {
	out := attemptOutcome{Retry: true}
	switch {
	case errors.Is(err, egress.ErrBlocked):
		out.Retry = false
		out.Attempt.Kind = string(errs.KindPermanent)
		out.Attempt.Error = string(errs.CodeEndpointURLBlocked)
	case errors.Is(err, context.DeadlineExceeded):
		out.Attempt.Kind = string(errs.KindTemporary)
		out.Attempt.Error = string(errs.CodeWebhookTimeout)
	default:
		var certErr *tls.CertificateVerificationError
		var unknownAuthority x509.UnknownAuthorityError
		if errors.As(err, &certErr) || errors.As(err, &unknownAuthority) || strings.Contains(strings.ToLower(err.Error()), "tls") {
			out.Attempt.Kind = string(errs.KindTemporary)
			out.Attempt.Error = string(errs.CodeWebhookTLSFailed)
		} else {
			out.Attempt.Kind = string(errs.KindTemporary)
			out.Attempt.Error = string(errs.CodeWebhookHTTPError)
		}
	}
	return out
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return min(time.Duration(seconds)*time.Second, maxBackoff)
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	delay := at.Sub(now)
	if delay <= 0 {
		return 0
	}
	return min(delay, maxBackoff)
}

func retryDelay(attempt int, base time.Duration, jitter, unit float64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if base <= 0 {
		base = 5 * time.Second
	}
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	if unit < 0 {
		unit = 0
	}
	if unit > 1 {
		unit = 1
	}
	exp := math.Pow(2, float64(attempt-1))
	delay := min(time.Duration(float64(base)*exp), maxBackoff)
	factor := 1 - jitter + 2*jitter*unit
	return min(time.Duration(float64(delay)*factor), maxBackoff)
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

func statusError(status int) string {
	return fmt.Sprintf("HTTP %d %s", status, http.StatusText(status))
}
