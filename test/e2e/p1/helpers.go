//go:build e2e

package p1

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
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
		"maxAttempts": 3, "backoffBase": "200ms", "backoffJitter": 0.0, "timeout": "3s",
	}
}

func startFixture(t *testing.T, switches map[string]any, smtpPolicy string, sinkOpts sink.Options, events []string) *fixture {
	t.Helper()
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	user := fmt.Sprintf("u%d", time.Now().UnixNano())
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)

	f := &fixture{t: t, Node: n, Sim: sim, Owner: owner, User: user, Pass: pass, Email: email}

	if events == nil {
		events = []string{"*"}
	}
	// Development defaults turn sandbox on + capture; send scenarios need real SMTP unless they opt in.
	sw := map[string]any{"sandbox.enabled": false}
	for k, v := range switches {
		sw[k] = v
	}
	f.Secret = harness.WebhookSecret()
	f.Sink = harness.StartSink(t, sinkOpts)
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name":    "e2e-" + user,
		"kind":    "http",
		"url":     f.Sink.URL,
		"events":  events,
		"secret":  f.Secret,
		"headers": []map[string]any{{"name": "X-E2E-Hook", "value": "requested"}},
		"payload": map[string]any{"text": "plain", "headers": []string{"Subject", "Message-ID"}},
		"retry":   defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	f.EPID, _ = ep.JSON["id"].(string)
	if f.EPID == "" {
		t.Fatalf("endpoint: %s", ep.Body)
	}

	body := harness.AccountCreateBody(t, sim, user, pass, email, "E2E "+user, sw, smtpPolicy)
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	f.AccID = acc.Str("id")
	if f.AccID == "" {
		t.Fatalf("account: %s", acc.Body)
	}
	f.waitAccountStates(60*time.Second, "connected", "syncing")
	return f
}

func (f *fixture) waitAccountStates(timeout time.Duration, want ...string) {
	f.t.Helper()
	set := map[string]bool{}
	for _, w := range want {
		set[w] = true
	}
	f.Owner.WaitJSON(f.t, "/v1/accounts/"+f.AccID, timeout, "account state", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && set[r.Str("state")]
	})
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

func (f *fixture) setSMTPScript(script simulator.SMTPScript) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.Sim.Client.SetSMTPScript(ctx, f.User, script); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) clearSMTPScript() {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.Sim.Client.ClearSMTPScript(ctx, f.User); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) smtpCount() int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msgs, err := f.Sim.Client.ListSMTPMessages(ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	return len(msgs)
}

func (f *fixture) submit(payload map[string]any, extraHdr map[string]string) harness.Resp {
	f.t.Helper()
	prev := f.Owner.Hdr
	hdr := map[string]string{}
	for k, v := range prev {
		hdr[k] = v
	}
	for k, v := range extraHdr {
		hdr[k] = v
	}
	f.Owner.Hdr = hdr
	r := f.Owner.Do(http.MethodPost, "/v1/accounts/"+f.AccID+"/outbound-messages", payload)
	f.Owner.Hdr = prev
	return r
}
