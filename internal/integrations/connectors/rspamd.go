package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RspamdResult is the subset of /checkv2 used on message.received.spam.
type RspamdResult struct {
	Score   float64            `json:"score"`
	Action  string             `json:"action"`
	Symbols map[string]float64 `json:"symbols"`
}

// RspamdOptions configures a checkv2 call.
type RspamdOptions struct {
	BaseURL  string
	Password string
	Timeout  time.Duration
}

// RspamdCheck POSTs raw MIME to Rspamd /checkv2 (P8.3). Sync engine wiring is a HOTSPOT.
func RspamdCheck(ctx context.Context, client *http.Client, opts RspamdOptions, raw []byte) (RspamdResult, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		return RspamdResult{}, fmt.Errorf("rspamd: baseUrl required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, base+"/checkv2", strings.NewReader(string(raw)))
	if err != nil {
		return RspamdResult{}, err
	}
	req.Header.Set("Content-Type", "message/rfc822")
	if pw := strings.TrimSpace(opts.Password); pw != "" {
		req.Header.Set("Password", pw)
	}
	res, err := client.Do(req)
	if err != nil {
		return RspamdResult{}, err
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return RspamdResult{}, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return RspamdResult{}, fmt.Errorf("rspamd: HTTP %d: %s", res.StatusCode, truncate(string(body), 200))
	}
	var rawJSON struct {
		Score   float64                    `json:"score"`
		Action  string                     `json:"action"`
		Symbols map[string]json.RawMessage `json:"symbols"`
	}
	if err := json.Unmarshal(body, &rawJSON); err != nil {
		return RspamdResult{}, err
	}
	out := RspamdResult{Score: rawJSON.Score, Action: rawJSON.Action, Symbols: map[string]float64{}}
	for name, rawSym := range rawJSON.Symbols {
		var asObj struct {
			Score float64 `json:"score"`
		}
		if err := json.Unmarshal(rawSym, &asObj); err == nil && (asObj.Score != 0 || string(rawSym) == `{"score":0}` || strings.Contains(string(rawSym), "score")) {
			out.Symbols[name] = asObj.Score
			continue
		}
		var asNum float64
		if err := json.Unmarshal(rawSym, &asNum); err == nil {
			out.Symbols[name] = asNum
		}
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
