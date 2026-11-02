package authserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func signBody(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func (r *Resolver) fetch(ctx context.Context, url, secret string, timeout time.Duration, reqBody Request) (Creds, error) {
	body, err := json.Marshal(reqBody)
	if err != nil {
		return Creds{}, errs.Wrap(err, errs.CodeInternalError, "authserver.marshal")
	}
	ts := r.clock().Unix()
	sig := signBody(secret, ts, body)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Creds{}, errs.Wrap(err, errs.CodeAuthServerMisconfigured, "authserver.url")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-EB-Timestamp", strconv.FormatInt(ts, 10))
	httpReq.Header.Set("X-EB-Signature", sig)

	cli := r.httpClient(timeout)
	res, err := cli.Do(httpReq)
	if err != nil {
		return Creds{}, errs.Wrap(err, errs.CodeAuthServerUnavailable, "authserver.fetch")
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return Creds{}, errs.Wrap(err, errs.CodeAuthServerFailed, "authserver.read")
	}
	if res.StatusCode >= 400 {
		return Creds{}, errs.New(errs.CodeAuthServerFailed, "Credential server returned an error.").
			WithSafe("status", res.StatusCode)
	}
	var out Response
	if err := json.Unmarshal(raw, &out); err != nil {
		return Creds{}, errs.New(errs.CodeAuthServerFailed, "Credential server returned invalid JSON.")
	}
	creds := Creds{User: out.User, Pass: out.Pass, AccessToken: out.AccessToken}
	if creds.User == "" && out.Username != "" {
		creds.User = out.Username
	}
	if creds.Pass == "" && creds.AccessToken == "" {
		return Creds{}, errs.New(errs.CodeAuthServerFailed, "Credential server returned no credentials.")
	}
	if out.ExpiresIn > 0 {
		creds.ExpiresAt = r.clock().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return creds, nil
}

func (r *Resolver) httpClient(timeout time.Duration) *http.Client {
	if r.HTTP != nil {
		c := *r.HTTP
		c.Timeout = timeout
		return &c
	}
	if r.Egress != nil {
		return r.Egress.HTTPClient(timeout)
	}
	return &http.Client{Timeout: timeout}
}

// VerifySignature checks an inbound HMAC (for tests / simulators).
func VerifySignature(secret string, ts int64, body []byte, header string) bool {
	want := signBody(secret, ts, body)
	got := strings.TrimSpace(header)
	return hmac.Equal([]byte(want), []byte(got))
}
