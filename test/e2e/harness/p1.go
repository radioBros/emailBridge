//go:build e2e

package harness

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/pkg/webhookverify"
	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/control"
	"github.com/radiobros/emailbridge/test/sink"
)

// SimEnv is an in-process provider simulator for e2e scenarios.
type SimEnv struct {
	Sim    *simulator.Simulator
	Addr   simulator.Addresses
	Client *control.Client
}

// StartSim runs the simulator on random localhost ports and registers cleanup.
func StartSim(t *testing.T) *SimEnv {
	t.Helper()
	sim, err := simulator.New(simulator.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	addr := sim.Addr()
	return &SimEnv{Sim: sim, Addr: addr, Client: control.NewClient(addr.Control)}
}

// HostPort splits host:port; fatals on error.
func HostPort(t *testing.T, addr string) (host string, port int) {
	t.Helper()
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return h, n
}

// P1Env is the default environment for a full-role P1 node (api+sync+submit+webhook).
func P1Env(extra map[string]string) map[string]string {
	env := map[string]string{
		"EB_ROLES":                "all",
		"EB_EGRESS_ALLOW":         "127.0.0.1/32,::1/128",
		"EB_EGRESS_BLOCK_PRIVATE": "false",
	}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// StartP1Node starts a node with sync/submit/webhook roles and localhost egress allow.
func StartP1Node(t *testing.T, extraEnv map[string]string) *Node {
	t.Helper()
	return StartNode(t, P1Env(extraEnv))
}

// CreateAPIKey creates an API key with the given scopes and returns id + plaintext token.
func (c *Client) CreateAPIKey(t *testing.T, name string, scopes []string) (id, token string) {
	t.Helper()
	r := c.Do(http.MethodPost, "/v1/api-keys", map[string]any{"name": name, "scopes": scopes})
	ExpectStatus(t, r, http.StatusCreated)
	token, _ = r.JSON["token"].(string)
	id, _ = r.JSON["id"].(string)
	if token == "" || id == "" {
		t.Fatalf("create api key: %s", r.Body)
	}
	return id, token
}

// WithBearer returns a new client that sends Authorization: Bearer token.
func (n *Node) WithBearer(token string) *Client {
	c := n.NewClient()
	c.Hdr = map[string]string{"Authorization": "Bearer " + token}
	return c
}

// WebhookSecret returns a valid Standard Webhooks secret (whsec_ + 32 bytes).
func WebhookSecret() string {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return "whsec_" + base64.StdEncoding.EncodeToString(key)
}

// StartSink starts a scriptable webhook sink the node can reach.
func StartSink(t *testing.T, opts sink.Options) *sink.Server {
	t.Helper()
	s := sink.NewServer(opts)
	t.Cleanup(s.Close)
	return s
}

// WaitSink waits until the sink has at least n requests or fatals.
func WaitSink(t *testing.T, s *sink.Server, n int, timeout time.Duration) []sink.Request {
	t.Helper()
	if !s.WaitFor(n, timeout) {
		t.Fatalf("sink: want ≥%d requests within %s, got %d", n, timeout, len(s.Requests()))
	}
	return s.Requests()
}

// WaitUntil polls cond until true or timeout.
func WaitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %s", what)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// WaitJSON polls GET path until pred(Resp) is true.
func (c *Client) WaitJSON(t *testing.T, path string, timeout time.Duration, what string, pred func(Resp) bool) Resp {
	t.Helper()
	var last Resp
	WaitUntil(t, timeout, what, func() bool {
		last = c.Do(http.MethodGet, path, nil)
		return pred(last)
	})
	return last
}

// VerifyWebhook checks Standard Webhooks signatures on a sink request.
func VerifyWebhook(t *testing.T, secret string, req sink.Request) {
	t.Helper()
	if err := webhookverify.Verify(secret, req.Headers, req.Body, time.Now(), webhookverify.DefaultTolerance); err != nil {
		t.Fatalf("webhook signature: %v\nheaders=%v\nbody=%s", err, req.Headers, truncate(req.Body, 400))
	}
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// EventType extracts the CloudEvents / EB type from a webhook body or eb-event-type header.
func EventType(req sink.Request) string {
	if v := req.Headers.Get("eb-event-type"); v != "" {
		return v
	}
	var m map[string]any
	if json.Unmarshal(req.Body, &m) == nil {
		if t, ok := m["type"].(string); ok {
			return t
		}
	}
	return ""
}

// FilterEvents returns sink requests whose event type equals typ.
func FilterEvents(reqs []sink.Request, typ string) []sink.Request {
	var out []sink.Request
	for _, r := range reqs {
		if EventType(r) == typ {
			out = append(out, r)
		}
	}
	return out
}

// WaitEvent waits for at least one sink delivery of the given event type.
func WaitEvent(t *testing.T, s *sink.Server, typ string, timeout time.Duration) sink.Request {
	t.Helper()
	var found sink.Request
	WaitUntil(t, timeout, typ, func() bool {
		for _, r := range s.Requests() {
			if EventType(r) == typ {
				found = r
				return true
			}
		}
		return false
	})
	return found
}

// WaitDeliveryState polls GET /v1/deliveries until one matching pred reaches a final-ish state.
func (c *Client) WaitDeliveryState(t *testing.T, query string, want string, timeout time.Duration) map[string]any {
	t.Helper()
	var got map[string]any
	WaitUntil(t, timeout, "delivery state="+want, func() bool {
		r := c.Do(http.MethodGet, "/v1/deliveries?"+query, nil)
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m == nil {
				continue
			}
			if st, _ := m["state"].(string); st == want {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

// WaitOutboundState polls GET /v1/outbound-messages/{id} until state matches.
func (c *Client) WaitOutboundState(t *testing.T, id, want string, timeout time.Duration) Resp {
	t.Helper()
	return c.WaitJSON(t, "/v1/outbound-messages/"+id, timeout, "outbound "+want, func(r Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == want
	})
}

// AccountCreateBody builds a POST /v1/accounts body pointed at the simulator (plain IMAP/SMTP).
func AccountCreateBody(t *testing.T, sim *SimEnv, user, pass, email, name string, switches map[string]any, smtpPolicy string) map[string]any {
	t.Helper()
	imapHost, imapPort := HostPort(t, sim.Addr.IMAPPlain)
	smtpHost, smtpPort := HostPort(t, sim.Addr.SMTPPlain)
	if switches == nil {
		switches = map[string]any{}
	}
	if smtpPolicy == "" {
		smtpPolicy = "standard"
	}
	return map[string]any{
		"name":  name,
		"email": email,
		"imap": map[string]any{
			"host": imapHost, "port": imapPort, "tls": "none",
			"auth": map[string]any{"user": user, "pass": pass},
		},
		"smtp": map[string]any{
			"host": smtpHost, "port": smtpPort, "tls": "none",
			"auth":   map[string]any{"user": user, "pass": pass},
			"policy": smtpPolicy,
		},
		"sync": map[string]any{
			"paths": []string{"*"}, "idlePaths": []string{"INBOX"},
		},
		"switches": switches,
	}
}

// CreateSimUser creates a simulator mailbox user.
func (s *SimEnv) CreateSimUser(t *testing.T, user, pass string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.Client.CreateUser(ctx, user, pass); err != nil {
		t.Fatal(err)
	}
}

// InjectRaw injects an RFC822 message into a mailbox.
func (s *SimEnv) InjectRaw(t *testing.T, user, mailbox string, raw []byte, flags []string) *control.InjectResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.Client.InjectMessage(ctx, user, mailbox, raw, flags, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// RawMessage builds a minimal RFC822 message.
func RawMessage(from, to, subject, messageID, body string) []byte {
	if messageID == "" {
		messageID = fmt.Sprintf("<%d@e2e.test>", time.Now().UnixNano())
	}
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		from, to, subject, messageID, time.Now().UTC().Format(time.RFC1123Z), body,
	))
}

// SandboxExists reports whether internal/sandbox is present in the tree.
func SandboxExists() bool {
	_, err := os.Stat(filepath.Join(FindRepoRoot(), "internal", "sandbox"))
	return err == nil
}

// StartSimOpts runs the provider simulator with options (P9: the PEC mode).
func StartSimOpts(t *testing.T, opts simulator.Options) *SimEnv {
	t.Helper()
	sim, err := simulator.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	addr := sim.Addr()
	return &SimEnv{Sim: sim, Addr: addr, Client: control.NewClient(addr.Control)}
}
