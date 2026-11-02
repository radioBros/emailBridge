//go:build e2e

package p4

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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

type fixtureOpts struct {
	switches map[string]any
	payload  map[string]any
	events   []string
	sync     map[string]any
	extraEnv map[string]string
}

func startFixture(t *testing.T, opts fixtureOpts) *fixture {
	t.Helper()
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, opts.extraEnv)
	owner, _ := n.OwnerSession(t)

	user := fmt.Sprintf("p4u%d", time.Now().UnixNano())
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)

	f := &fixture{t: t, Node: n, Sim: sim, Owner: owner, User: user, Pass: pass, Email: email}
	if opts.events == nil {
		opts.events = []string{"*"}
	}
	sw := map[string]any{"sandbox.enabled": false}
	for k, v := range opts.switches {
		sw[k] = v
	}
	payload := map[string]any{"text": "plain", "headers": []string{"Subject", "Message-ID"}, "attachments": true}
	for k, v := range opts.payload {
		payload[k] = v
	}
	f.Secret = harness.WebhookSecret()
	f.Sink = harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "e2e-p4-" + user, "kind": "http", "url": f.Sink.URL,
		"events": opts.events, "secret": f.Secret, "payload": payload, "retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	f.EPID = ep.Str("id")

	body := harness.AccountCreateBody(t, sim, user, pass, email, "P4 "+user, sw, "standard")
	if opts.sync != nil {
		sync, _ := body["sync"].(map[string]any)
		if sync == nil {
			sync = map[string]any{}
		}
		for k, v := range opts.sync {
			sync[k] = v
		}
		body["sync"] = sync
	}
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	f.AccID = acc.Str("id")
	f.waitConnected(90 * time.Second)
	return f
}

func (f *fixture) waitConnected(timeout time.Duration) {
	f.t.Helper()
	f.Owner.WaitJSON(f.t, "/v1/accounts/"+f.AccID, timeout, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && (r.Str("state") == "connected" || r.Str("state") == "syncing")
	})
	f.Owner.WaitJSON(f.t, "/v1/accounts/"+f.AccID, timeout, "connected steady", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
}

func (f *fixture) base() string {
	return "/v1/accounts/" + f.AccID
}

func (f *fixture) inject(subject, mid, body string) {
	f.t.Helper()
	raw := harness.RawMessage(f.Email, f.Email, subject, mid, body)
	f.Sim.InjectRaw(f.t, f.User, "INBOX", raw, nil)
}

func (f *fixture) injectRaw(mailbox string, raw []byte, flags []string, when time.Time) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if when.IsZero() {
		when = time.Now().UTC()
	}
	if _, err := f.Sim.Client.InjectMessage(ctx, f.User, mailbox, raw, flags, when); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) waitMsgBySubject(subject string, timeout time.Duration) map[string]any {
	f.t.Helper()
	var found map[string]any
	f.Owner.WaitJSON(f.t, f.base()+"/messages?limit=50&includeTotal=true", timeout, "msg "+subject, func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m != nil && m["subject"] == subject {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

func (f *fixture) inboxID() string {
	f.t.Helper()
	r := f.Owner.Do(http.MethodGet, f.base()+"/mailboxes?path=INBOX", nil)
	harness.ExpectStatus(f.t, r, http.StatusOK)
	data, _ := r.JSON["data"].([]any)
	for _, raw := range data {
		m, _ := raw.(map[string]any)
		if m != nil && m["path"] == "INBOX" {
			id, _ := m["id"].(string)
			if id != "" {
				return id
			}
		}
	}
	f.t.Fatalf("INBOX not indexed: %s", r.Body)
	return ""
}

func (f *fixture) mailboxID(path string) string {
	f.t.Helper()
	var id string
	f.Owner.WaitJSON(f.t, f.base()+"/mailboxes?limit=100", 60*time.Second, "mailbox "+path, func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m != nil && m["path"] == path {
				id, _ = m["id"].(string)
				return id != ""
			}
		}
		return false
	})
	return id
}

func eventData(t *testing.T, req sink.Request) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal(req.Body, &env); err != nil {
		t.Fatal(err)
	}
	data, _ := env["data"].(map[string]any)
	if data == nil {
		t.Fatalf("no data in event: %s", req.Body)
	}
	return data
}

func multipartAtts(from, to, subject, mid string, files [][2]string) []byte {
	var b strings.Builder
	_, _ = fmt.Fprintf(&b,
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b1\r\n\r\n",
		from, to, subject, mid, time.Now().UTC().Format(time.RFC1123Z),
	)
	b.WriteString("--b1\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nSee attached.\r\n")
	for _, f := range files {
		name, body := f[0], f[1]
		b.WriteString("--b1\r\nContent-Type: text/plain; name=\"" + name + "\"\r\n")
		b.WriteString("Content-Disposition: attachment; filename=\"" + name + "\"\r\n\r\n")
		b.WriteString(body + "\r\n")
	}
	b.WriteString("--b1--\r\n")
	return []byte(b.String())
}

func htmlScriptMsg(from, to, subject, mid, html string) []byte {
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n"+
			"Content-Type: text/html; charset=utf-8\r\n\r\n%s\r\n",
		from, to, subject, mid, time.Now().UTC().Format(time.RFC1123Z), html,
	))
}

func calendarInvite(from, to, subject, mid string) []byte {
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nMETHOD:REQUEST\r\nPRODID:-//EB//E2E//EN\r\n" +
		"BEGIN:VEVENT\r\nUID:meet-e2e@eb.test\r\nSUMMARY:Standup\r\nSTATUS:CONFIRMED\r\n" +
		"DTSTART:20261027T090000Z\r\nDTEND:20261027T093000Z\r\n" +
		"ORGANIZER;CN=Boss:mailto:boss@eb.test\r\n" +
		"ATTENDEE;CN=You;ROLE=REQ-PARTICIPANT;PARTSTAT=NEEDS-ACTION:mailto:" + to + "\r\n" +
		"END:VEVENT\r\nEND:VCALENDAR\r\n"
	var b strings.Builder
	_, _ = fmt.Fprintf(&b,
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n"+
			"Content-Type: multipart/mixed; boundary=cal\r\n\r\n",
		from, to, subject, mid, time.Now().UTC().Format(time.RFC1123Z),
	)
	b.WriteString("--cal\r\nContent-Type: text/plain\r\n\r\nInvitation\r\n")
	b.WriteString("--cal\r\nContent-Type: text/calendar; method=REQUEST; charset=utf-8\r\nContent-Disposition: inline\r\n\r\n")
	b.WriteString(ics)
	b.WriteString("\r\n--cal--\r\n")
	return []byte(b.String())
}

func replyQuoted(from, to, subject, mid, newText string) []byte {
	body := newText + "\r\n\r\nOn Mon, 1 Jan 2026 boss@eb.test wrote:\r\n> old quoted line\r\n> more quote\r\n"
	return harness.RawMessage(from, to, subject, mid, body)
}

func eicarMIME(from, to, subject, mid string) []byte {
	eicar := `X5O!P%@AP[4\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*`
	var b strings.Builder
	_, _ = fmt.Fprintf(&b,
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\n"+
			"Content-Type: multipart/mixed; boundary=e1\r\n\r\n",
		from, to, subject, mid, time.Now().UTC().Format(time.RFC1123Z),
	)
	b.WriteString("--e1\r\nContent-Type: text/plain\r\n\r\nhi\r\n")
	b.WriteString("--e1\r\nContent-Type: application/octet-stream\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"eicar.com\"\r\n\r\n")
	b.WriteString(eicar + "\r\n--e1--\r\n")
	return []byte(b.String())
}

func b64(raw []byte) string { return base64.StdEncoding.EncodeToString(raw) }

func asStringSlice(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
