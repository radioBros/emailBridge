package gateways

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func doJSON(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, payload any) (status int, body []byte, err error) {
	var rdr io.Reader
	if payload != nil {
		b, e := json.Marshal(payload)
		if e != nil {
			return 0, nil, e
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return 0, nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "EmailBridge-Gateway/1")
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, raw, nil
}

func doForm(ctx context.Context, client *http.Client, rawURL string, headers map[string]string, form url.Values) (status int, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", "EmailBridge-Gateway/1")
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return res.StatusCode, raw, nil
}

func apiErr(provider string, status int, raw []byte) error {
	kind, code := ClassifyAPIError(status, string(raw))
	return fmt.Errorf("%s: HTTP %d kind=%s code=%s: %s", provider, status, kind, code, truncate(string(raw), 200))
}
