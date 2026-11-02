//go:build e2e

package p7

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

type fixture struct {
	t      *testing.T
	Node   *harness.Node
	Sim    *harness.SimEnv
	Owner  *harness.Client
	User   string
	Pass   string
	Email  string
	AccID  string
	Sink   *sink.Server
	Secret string
	EPID   string
}

func defaultRetry() map[string]any {
	return map[string]any{
		"maxAttempts": 3, "backoffBase": "200ms", "backoffJitter": 0.0, "timeout": "5s",
	}
}

// p7Env extends P1Env with localhost on the egress allow-list (AI local http://localhost mock).
func p7Env(extra map[string]string) map[string]string {
	env := harness.P1Env(map[string]string{
		"EB_EGRESS_ALLOW": "127.0.0.1/32,::1/128,localhost",
	})
	for k, v := range extra {
		env[k] = v
	}
	return env
}

func startFixture(t *testing.T, switches map[string]any, events []string, sinkOpts sink.Options) *fixture {
	t.Helper()
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, p7Env(nil))
	owner, _ := n.OwnerSession(t)

	user := fmt.Sprintf("p7u%d", time.Now().UnixNano())
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)

	f := &fixture{t: t, Node: n, Sim: sim, Owner: owner, User: user, Pass: pass, Email: email}
	if events == nil {
		events = []string{"*"}
	}
	sw := map[string]any{"sandbox.enabled": false}
	for k, v := range switches {
		sw[k] = v
	}
	f.Secret = harness.WebhookSecret()
	f.Sink = harness.StartSink(t, sinkOpts)
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "e2e-p7-" + user, "kind": "http", "url": f.Sink.URL,
		"events": events, "secret": f.Secret, "retry": defaultRetry(),
		"payload": map[string]any{"text": "plain", "headers": []string{"Subject", "Message-ID"}},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	f.EPID = ep.Str("id")
	if f.EPID == "" {
		t.Fatalf("endpoint: %s", ep.Body)
	}
	body := harness.AccountCreateBody(t, sim, user, pass, email, "P7 "+user, sw, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	f.AccID = acc.Str("id")
	if f.AccID == "" {
		t.Fatalf("account: %s", acc.Body)
	}
	f.waitConnected(60 * time.Second)
	return f
}

func (f *fixture) waitConnected(timeout time.Duration) {
	f.t.Helper()
	f.Owner.WaitJSON(f.t, "/v1/accounts/"+f.AccID, timeout, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
}

func (f *fixture) inject(subject, mid, body string) {
	f.t.Helper()
	raw := harness.RawMessage(f.Email, f.Email, subject, mid, body)
	f.Sim.InjectRaw(f.t, f.User, "INBOX", raw, nil)
}

func enableOrgSwitch(t *testing.T, owner *harness.Client, name string, value bool) {
	t.Helper()
	r := owner.Do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{
		"switches": map[string]any{name: map[string]any{"value": value}},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("enable %s: %d %s", name, r.Status, r.Body)
	}
}

func patchOrgEnv(t *testing.T, owner *harness.Client, env map[string]any) {
	t.Helper()
	r := owner.Do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{"env": env})
	if r.Status != http.StatusOK {
		t.Fatalf("patch env: %d %s", r.Status, r.Body)
	}
}

func problemCode(r harness.Resp) string {
	if c := r.Str("code"); c != "" {
		return c
	}
	return ""
}

func waitMessageID(t *testing.T, owner *harness.Client, accID, subject string, timeout time.Duration) string {
	t.Helper()
	var id string
	harness.WaitUntil(t, timeout, "message "+subject, func() bool {
		r := owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/messages?limit=20&sort=-internalDate", nil)
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			if s, _ := m["subject"].(string); s == subject {
				id, _ = m["id"].(string)
				return id != ""
			}
		}
		return false
	})
	return id
}

type headerRoundTripper struct {
	base http.RoundTripper
	hdr  map[string]string
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h.hdr {
		req.Header.Set(k, v)
	}
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func mcpSession(t *testing.T, base, token string) *sdkmcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	cli := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "e2e-p7", Version: "0"}, nil)
	httpCli := &http.Client{
		Timeout: 30 * time.Second,
		Transport: headerRoundTripper{
			hdr: map[string]string{"Authorization": "Bearer " + token},
		},
	}
	sess, err := cli.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             strings.TrimRight(base, "/") + "/mcp",
		HTTPClient:           httpCli,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("mcp connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func mcpText(r *sdkmcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	if tc, ok := r.Content[0].(*sdkmcp.TextContent); ok {
		return tc.Text
	}
	b, _ := json.Marshal(r.Content)
	return string(b)
}

// openSSE starts GET /v1/events/stream and returns the live response body (caller closes).
func openSSE(t *testing.T, owner *harness.Client, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, owner.N.Base+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", owner.N.Base)
	req.Header.Set("X-Requested-With", "EmailBridge")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range owner.Hdr {
		req.Header.Set(k, v)
	}
	res, err := owner.C.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		t.Fatalf("sse status %d %s", res.StatusCode, b)
	}
	if !strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		_ = res.Body.Close()
		t.Fatalf("sse content-type %q", res.Header.Get("Content-Type"))
	}
	return res
}

func waitSSEEvent(t *testing.T, body io.Reader, wantType string, timeout time.Duration) {
	t.Helper()
	done := make(chan string, 8)
	go func() {
		defer close(done)
		buf := make([]byte, 0, 8192)
		tmp := make([]byte, 2048)
		for {
			n, err := body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				for {
					i := strings.Index(string(buf), "\n\n")
					if i < 0 {
						break
					}
					block := string(buf[:i])
					buf = buf[i+2:]
					ev := ""
					for _, line := range strings.Split(block, "\n") {
						if strings.HasPrefix(line, "event:") {
							ev = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
						}
					}
					if ev != "" {
						select {
						case done <- ev:
						default:
						}
						if ev == wantType {
							return
						}
					}
				}
			}
			if err != nil {
				return
			}
		}
	}()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-done:
			if !ok {
				t.Fatalf("sse: stream ended before %s", wantType)
			}
			if ev == wantType {
				return
			}
		case <-deadline:
			t.Fatalf("sse: timeout waiting for %s", wantType)
		}
	}
}

func localhostBaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := u.Hostname()
	if host == "127.0.0.1" || host == "::1" {
		u.Host = "localhost:" + u.Port()
	}
	return u.String()
}
