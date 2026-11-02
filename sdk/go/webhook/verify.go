// Package webhook implements Standard Webhooks verification and EmailBridge's bodyOnly mode (S7 §2.2–2.3).
// This file is a copy of pkg/webhookverify; TestInSyncWithServer fails if they drift.
package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	HeaderWebhookID        = "webhook-id"
	HeaderWebhookTimestamp = "webhook-timestamp"
	HeaderWebhookSignature = "webhook-signature"
	HeaderEBSignature      = "eb-signature"

	secretPrefix = "whsec_"

	// DefaultTolerance is 5 minutes both directions (S7 §2.2).
	DefaultTolerance = 5 * time.Minute
)

var (
	// ErrMissingHeaders is returned when required signature headers are absent.
	ErrMissingHeaders = errors.New("webhook: missing headers")
	// ErrTimestamp is returned when the timestamp is outside the tolerance window.
	ErrTimestamp = errors.New("webhook: timestamp out of tolerance")
	// ErrSignature is returned when no signature matches.
	ErrSignature = errors.New("webhook: signature mismatch")
)

// Verify checks Standard Webhooks headers against the body.
// secret is whsec_<base64>. tolerance <= 0 uses DefaultTolerance.
func Verify(secret string, headers http.Header, body []byte, now time.Time, tolerance time.Duration) error {
	if tolerance <= 0 {
		tolerance = DefaultTolerance
	}
	id := headers.Get(HeaderWebhookID)
	tsHdr := headers.Get(HeaderWebhookTimestamp)
	sigHdr := headers.Get(HeaderWebhookSignature)
	if id == "" || tsHdr == "" || sigHdr == "" {
		return ErrMissingHeaders
	}
	tsUnix, err := strconv.ParseInt(tsHdr, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrTimestamp, err)
	}
	ts := time.Unix(tsUnix, 0)
	if now.IsZero() {
		now = time.Now()
	}
	delta := now.Sub(ts)
	if delta < 0 {
		delta = -delta
	}
	if delta > tolerance {
		return ErrTimestamp
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return err
	}
	expected := signBytes(key, id, ts, body)
	return matchSignatures(sigHdr, expected)
}

// VerifyBodyOnly checks eb-signature: v1,<base64(HMAC-SHA256(secret, body))> (S7 §2.3).
// secret is used as the raw HMAC key (not whsec_-decoded).
func VerifyBodyOnly(secret string, headers http.Header, body []byte) error {
	sigHdr := headers.Get(HeaderEBSignature)
	if sigHdr == "" {
		return ErrMissingHeaders
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return matchSignatures(sigHdr, expected)
}

// Sign returns v1,<base64> for Standard Webhooks (used by the server and tests).
func Sign(secret, id string, ts time.Time, body []byte) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	sig := signBytes(key, id, ts, body)
	return "v1," + sig, nil
}

// SignBodyOnly returns v1,<base64> for bodyOnly mode.
func SignBodyOnly(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// Middleware verifies Standard Webhooks signatures and restores the request body.
func Middleware(secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		if err := Verify(secret, r.Header, body, time.Now(), DefaultTolerance); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func decodeSecret(secret string) ([]byte, error) {
	s := strings.TrimPrefix(secret, secretPrefix)
	key, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("webhook: invalid secret: %w", err)
	}
	if len(key) == 0 {
		return nil, errors.New("webhook: empty secret")
	}
	return key, nil
}

func signBytes(key []byte, id string, ts time.Time, body []byte) string {
	toSign := fmt.Sprintf("%s.%d.%s", id, ts.Unix(), body)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// matchSignatures accepts a space-separated list of v1,<base64>; any match passes.
// Comparison is constant-time on the base64 payload (matches the official library).
func matchSignatures(header, expectedBase64 string) error {
	for _, part := range strings.Split(header, " ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		version, sig, ok := strings.Cut(part, ",")
		if !ok || version != "v1" {
			continue
		}
		if hmac.Equal([]byte(sig), []byte(expectedBase64)) {
			return nil
		}
	}
	return ErrSignature
}
