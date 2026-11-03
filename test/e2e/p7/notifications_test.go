//go:build e2e

package p7

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// System notifications (W75), end to end on a real node:
//   - a failing webhook endpoint raises ONE notification (bumped, never duplicated), the bell summary and
//     the realtime message arrive, an email digest reaches the local SMTP sink (never a real server), and the
//     endpoint's recovery resolves it;
//   - a stalled (paused) queue and an account auth error raise their problems;
//   - organisation B never sees organisation A's problems; a viewer never sees instance problems, neither in
//     the list nor over the WebSocket;
//   - email is sent only when the notifications.email switch (global → organisation, lockable) AND the
//     user's notifications.email.enabled are both on; in-app always works.
//
// The owner signs in as the allow-listed test address, but system mail goes to a loopback SMTP sink only.

const notifyOwnerEmail = "test@example.com"

func TestP7_Notifications(t *testing.T) {
	smtp := startSMTPSink(t)
	sim := harness.StartSim(t)
	host, port := harness.HostPort(t, smtp.addr)
	n := harness.StartP1Node(t, p7Env(map[string]string{
		"EB_BOOTSTRAP_OWNER_EMAIL": notifyOwnerEmail,
		"EB_SYSMAIL_MODE":          "smtp",
		"EB_SYSMAIL_ALLOW_LIST":    notifyOwnerEmail,
		"EB_SYSMAIL_FROM":          "emailbridge@e2e.test",
		"EB_SYSMAIL_SMTP_HOST":     host,
		"EB_SYSMAIL_SMTP_PORT":     strconv.Itoa(port),
		"EB_SYSMAIL_SMTP_TLS":      "none",
	}))
	owner := signInEnrolled(t, n, notifyOwnerEmail, harness.OwnerPassword)
	me := owner.Do(http.MethodGet, "/v1/me", nil)
	harness.ExpectStatus(t, me, http.StatusOK)
	orgA := me.Str("activeOrganizationId")
	if orgA == "" {
		t.Fatalf("no active organisation: %s", me.Body)
	}

	// Fast watchers for the test (runtime settings, instance-wide).
	r := owner.Do(http.MethodPatch, "/v1/settings?scope=global", map[string]any{"values": map[string]any{
		"notifications.watchers.interval":         "5s",
		"notifications.webhooks.failureThreshold": 2,
		"notifications.queues.pausedLongerThan":   "5s",
		"notifications.queues.pendingMinutes":     "5s",
		"notifications.queues.oldestAge":          "1h",
	}})
	harness.ExpectStatus(t, r, http.StatusOK)

	ownerWS := dialNotifications(t, n, owner)

	// A connected mailbox and a webhook endpoint that answers 500.
	hook := harness.StartSink(t, sink.Options{Status: http.StatusInternalServerError})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "e2e-notify", "kind": "http", "url": hook.URL, "events": []string{"message.received"},
		"secret": harness.WebhookSecret(), "retry": defaultRetry(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	epID := ep.Str("id")
	user := fmt.Sprintf("ntf%d", time.Now().UnixNano())
	sim.CreateSimUser(t, user, "sim-secret")
	acc := owner.Do(http.MethodPost, "/v1/accounts", harness.AccountCreateBody(t, sim, user, "sim-secret",
		user+"@sim.e2e.test", "Notify "+user, map[string]any{"sandbox.enabled": false}, "standard"))
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	inject := func(subject string) {
		raw := harness.RawMessage(user+"@sim.e2e.test", user+"@sim.e2e.test", subject, "<"+subject+"@e2e.test>", "body")
		sim.InjectRaw(t, user, "INBOX", raw, nil)
	}

	// --- 1. Failing webhook → exactly one problem, realtime, summary, email digest. ---
	inject("ntf-fail-1")
	failKey := "webhook.failing:" + epID
	first := waitNotification(t, owner, "category=webhooks", failKey, "open", 90*time.Second)
	// Link is the admin list route: there is no /webhooks/:id page (sanitizeLink / watchers).
	if first["severity"] == nil || first["link"] != "/webhooks" {
		t.Fatalf("webhook problem: %v", first)
	}
	if data, _ := first["data"].(map[string]any); data == nil || data["endpointId"] != epID {
		t.Fatalf("webhook problem data: %v", first["data"])
	}
	inject("ntf-fail-2")
	harness.WaitUntil(t, 60*time.Second, "occurrences bump", func() bool {
		rows := listNotifications(t, owner, "category=webhooks")
		return countKey(rows, failKey) == 1 && occurrences(rows, failKey) >= 2
	})
	if c := countKey(listNotifications(t, owner, "category=webhooks&state="), failKey); c != 1 {
		t.Fatalf("one problem per endpoint, got %d rows", c)
	}
	ownerWS.waitFor(t, 30*time.Second, "realtime raised", func(m wsMsg) bool {
		return m.Type == "notification.raised" && m.Data["key"] == failKey
	})
	sum := owner.Do(http.MethodGet, "/v1/notifications/summary", nil)
	harness.ExpectStatus(t, sum, http.StatusOK)
	if u, _ := sum.JSON["unread"].(float64); u < 1 {
		t.Fatalf("summary unread: %s", sum.Body)
	}
	harness.WaitUntil(t, 60*time.Second, "email digest at the SMTP sink", func() bool { return smtp.count() >= 1 })
	smtp.onlyTo(t, notifyOwnerEmail)

	// Mark read: the bell count drops.
	harness.ExpectStatus(t, owner.Do(http.MethodPost, "/v1/notifications/read-all", nil), http.StatusNoContent)
	after := owner.Do(http.MethodGet, "/v1/notifications/summary", nil)
	if u, _ := after.JSON["unread"].(float64); u != 0 {
		t.Fatalf("after read-all: %s", after.Body)
	}

	// Recovery: the endpoint answers 200 again, a delivery succeeds, the problem resolves.
	hook.SetOptions(sink.Options{Status: http.StatusOK})
	inject("ntf-ok-1")
	waitNotification(t, owner, "category=webhooks&state=resolved", failKey, "resolved", 90*time.Second)
	ownerWS.waitFor(t, 30*time.Second, "realtime resolved", func(m wsMsg) bool {
		return m.Type == "notification.resolved" && m.Data["key"] == failKey
	})

	// --- 2. A stalled queue (paused) → instance problem for the instance owner; resume resolves it. ---
	harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/queues/EMAILBRIDGE_DELIVERY", map[string]any{"paused": true}), http.StatusOK)
	inject("ntf-paused-1")
	pauseKey := "queue.paused:EMAILBRIDGE_DELIVERY"
	qp := waitNotification(t, owner, "category=queues", pauseKey, "open", 60*time.Second)
	if qp["scope"] != "instance" {
		t.Fatalf("queue problem scope: %v", qp)
	}

	// --- 3. An account with a wrong password → account auth problem. ---
	sim.CreateSimUser(t, user+"b", "right-pass")
	badBody := harness.AccountCreateBody(t, sim, user+"b", "wrong-pass", user+"b@sim.e2e.test", "Bad "+user,
		map[string]any{"sandbox.enabled": false}, "standard")
	badBody["skipVerify"] = true
	bad := owner.Do(http.MethodPost, "/v1/accounts", badBody)
	harness.ExpectStatus(t, bad, http.StatusCreated)
	authKey := "account.auth:" + bad.Str("id")
	auth := waitNotification(t, owner, "category=accounts", authKey, "open", 90*time.Second)
	if auth["severity"] != "critical" {
		t.Fatalf("auth problem: %v", auth)
	}

	// --- 4. Visibility: a viewer of org A sees org problems, never instance ones; org B sees none of A's. ---
	viewer := invitedMember(t, n, orgA, "viewer")
	viewerWS := dialNotifications(t, n, viewer)
	vrows := listNotifications(t, viewer, "state=")
	if countKey(vrows, authKey) != 1 {
		t.Fatalf("viewer must see org problems: %v", keys(vrows))
	}
	for _, row := range vrows {
		if row["scope"] == "instance" {
			t.Fatalf("viewer sees an instance problem: %v", row)
		}
	}
	ob := owner.Do(http.MethodPost, "/v1/organizations", map[string]any{"name": "Org B Notify", "slug": fmt.Sprintf("org-b-ntf-%d", time.Now().UnixNano()%1_000_000)})
	harness.ExpectStatus(t, ob, http.StatusCreated)
	orgB := ob.Str("id")
	memberB := invitedMember(t, n, orgB, "owner")
	memberB.Hdr = map[string]string{"EB-Organization": orgB}
	for _, row := range listNotifications(t, memberB, "state=") {
		if row["organizationId"] == orgA || row["scope"] == "instance" {
			t.Fatalf("org B sees %v", row)
		}
	}
	bsum := memberB.Do(http.MethodGet, "/v1/notifications/summary", nil)
	if o, _ := bsum.JSON["open"].(float64); o != 0 {
		t.Fatalf("org B summary: %s", bsum.Body)
	}

	// Resume the queue: the instance problem resolves; the viewer's socket never carried it.
	harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/queues/EMAILBRIDGE_DELIVERY", map[string]any{"paused": false}), http.StatusOK)
	waitNotification(t, owner, "category=queues&state=resolved", pauseKey, "resolved", 60*time.Second)
	ownerWS.waitFor(t, 30*time.Second, "owner gets the instance resolve", func(m wsMsg) bool {
		return m.Type == "notification.resolved" && m.Data["key"] == pauseKey
	})
	// The viewer's socket is live (it gets the org problem's bumps from the account watcher every tick), so
	// the absence of instance messages — published to org A on every tick while the queue was paused — counts.
	viewerWS.waitFor(t, 30*time.Second, "viewer gets org-scope updates", func(m wsMsg) bool {
		return m.Data["key"] == authKey
	})
	for _, m := range viewerWS.all() {
		if m.Data["scope"] == "instance" {
			t.Fatalf("viewer socket received an instance problem: %v", m)
		}
	}

	// --- 5. Email toggles: switch (org / global locked) × user toggle; in-app always on. ---
	patchMe := func(email map[string]any) {
		t.Helper()
		harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/me/settings",
			map[string]any{"notifications": map[string]any{"email": email}}), http.StatusOK)
	}
	patchSwitch := func(scope string, v any) {
		t.Helper()
		harness.ExpectStatus(t, owner.Do(http.MethodPatch, "/v1/settings?scope="+scope,
			map[string]any{"switches": map[string]any{"notifications.email": v}}), http.StatusOK)
	}
	expiring := func(name string) string {
		t.Helper()
		k := owner.Do(http.MethodPost, "/v1/api-keys", map[string]any{
			"name": name, "scopes": []string{"accounts:read"}, "expiresAt": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339),
		})
		harness.ExpectStatus(t, k, http.StatusCreated)
		key := "security.apikey:" + k.Str("id")
		waitNotification(t, owner, "category=security", key, "open", 60*time.Second) // in-app regardless of email
		return key
	}
	quiet := func(what string, base int) {
		t.Helper()
		time.Sleep(12 * time.Second) // two watcher / digest ticks
		if got := smtp.count(); got != base {
			t.Fatalf("%s: %d emails, want %d", what, got, base)
		}
	}
	patchMe(map[string]any{"digest": "immediate", "minSeverity": "warning"})
	// Let the immediate-digest catch-up of earlier problems go out: the count must be stable over two ticks.
	base := smtp.count()
	for stable := 0; stable < 2; {
		time.Sleep(6 * time.Second)
		if c := smtp.count(); c == base {
			stable++
		} else {
			base, stable = c, 0
		}
	}

	patchSwitch("organization", map[string]any{"value": false})
	expiring("ntf-org-off")
	quiet("organisation switch off", base)

	patchSwitch("organization", map[string]any{"value": true})
	patchMe(map[string]any{"enabled": false})
	expiring("ntf-user-off")
	quiet("user toggle off", base)

	patchMe(map[string]any{"enabled": true})
	patchSwitch("global", map[string]any{"value": false, "locked": true})
	expiring("ntf-global-locked-off")
	quiet("global switch locked off (organisation on)", base)

	patchSwitch("global", nil)
	harness.WaitUntil(t, 60*time.Second, "email once switch and user toggle are both on", func() bool { return smtp.count() > base })
	smtp.onlyTo(t, notifyOwnerEmail)
}

// --- helpers ---

func signInEnrolled(t *testing.T, n *harness.Node, email, pass string) *harness.Client {
	t.Helper()
	c := n.NewClient()
	harness.ExpectStatus(t, c.SignIn(email, pass), http.StatusOK)
	secret, used := c.EnrolTOTP(pass)
	return n.SignInWithTOTP(t, email, pass, secret, used)
}

// invitedMember adds a user to org with role through an invitation and returns a signed-in client.
func invitedMember(t *testing.T, n *harness.Node, org, role string) *harness.Client {
	t.Helper()
	email := fmt.Sprintf("%s-%d@e2e.test", role, time.Now().UnixNano()%1_000_000_000)
	token := n.SeedInvite(t, org, email, role)
	r := n.NewClient().Do(http.MethodPost, "/v1/invitations/accept", map[string]any{
		"token": token, "password": harness.OwnerPassword, "name": "Member " + role,
	})
	if r.Status != http.StatusOK && r.Status != http.StatusCreated {
		t.Fatalf("accept invite = %d %s", r.Status, r.Body)
	}
	c := signInEnrolled(t, n, email, harness.OwnerPassword)
	c.Hdr = map[string]string{"EB-Organization": org}
	return c
}

func listNotifications(t *testing.T, c *harness.Client, query string) []map[string]any {
	t.Helper()
	r := c.Do(http.MethodGet, "/v1/notifications?limit=200&"+query, nil)
	harness.ExpectStatus(t, r, http.StatusOK)
	data, _ := r.JSON["data"].([]any)
	out := make([]map[string]any, 0, len(data))
	for _, d := range data {
		if m, ok := d.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func waitNotification(t *testing.T, c *harness.Client, query, key, state string, timeout time.Duration) map[string]any {
	t.Helper()
	var found map[string]any
	harness.WaitUntil(t, timeout, key+" "+state, func() bool {
		for _, row := range listNotifications(t, c, query) {
			if row["key"] == key && row["state"] == state {
				found = row
				return true
			}
		}
		return false
	})
	return found
}

func countKey(rows []map[string]any, key string) int {
	n := 0
	for _, row := range rows {
		if row["key"] == key {
			n++
		}
	}
	return n
}

func occurrences(rows []map[string]any, key string) float64 {
	for _, row := range rows {
		if row["key"] == key {
			o, _ := row["occurrences"].(float64)
			return o
		}
	}
	return 0
}

func keys(rows []map[string]any) []any {
	out := make([]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, row["key"])
	}
	return out
}

// wsMsg is one realtime message on the notifications topic.
type wsMsg struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type wsCollector struct {
	mu   sync.Mutex
	msgs []wsMsg
}

func (w *wsCollector) all() []wsMsg {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]wsMsg(nil), w.msgs...)
}

func (w *wsCollector) waitFor(t *testing.T, timeout time.Duration, what string, pred func(wsMsg) bool) {
	t.Helper()
	harness.WaitUntil(t, timeout, what, func() bool {
		for _, m := range w.all() {
			if pred(m) {
				return true
			}
		}
		return false
	})
}

// dialNotifications opens the admin WebSocket for c, subscribes to the notifications topic and collects
// every message in the background until the test ends.
func dialNotifications(t *testing.T, n *harness.Node, c *harness.Client) *wsCollector {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	hdr := http.Header{}
	hdr.Set("Origin", n.Base)
	for k, v := range c.Hdr {
		hdr.Set(k, v)
	}
	u, _ := strings.CutPrefix(n.Base, "http")
	conn, res, err := websocket.Dial(ctx, "ws"+u+"/v1/admin/ws", &websocket.DialOptions{HTTPHeader: hdr, HTTPClient: c.C})
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	if res != nil && res.Body != nil {
		_ = res.Body.Close()
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	sub, _ := json.Marshal(map[string]any{"op": "sub", "id": "n1", "topic": "notifications"})
	if err := conn.Write(ctx, websocket.MessageText, sub); err != nil {
		t.Fatal(err)
	}
	rctx, rcancel := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := conn.Read(rctx)
	rcancel()
	if err != nil {
		t.Fatal(err)
	}
	var ack map[string]any
	_ = json.Unmarshal(data, &ack)
	if ack["op"] != "subscribed" {
		t.Fatalf("subscribe notifications: %s", data)
	}
	col := &wsCollector{}
	go func() { // the hub closes sockets idle for 60 s: ping like the admin UI does
		tk := time.NewTicker(20 * time.Second)
		defer tk.Stop()
		ping, _ := json.Marshal(map[string]any{"op": "ping"})
		for {
			select {
			case <-ctx.Done():
				return
			case <-tk.C:
				if conn.Write(ctx, websocket.MessageText, ping) != nil {
					return
				}
			}
		}
	}()
	go func() {
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var m wsMsg
			if json.Unmarshal(data, &m) == nil && m.Type != "" {
				col.mu.Lock()
				col.msgs = append(col.msgs, m)
				col.mu.Unlock()
			}
		}
	}()
	return col
}

// smtpSink is a minimal loopback SMTP server that records every message (system mail never leaves the test).
type smtpSink struct {
	addr string
	mu   sync.Mutex
	rcpt [][]string
}

func startSMTPSink(t *testing.T) *smtpSink {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	s := &smtpSink{addr: l.Addr().String()}
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *smtpSink) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(time.Minute))
	rd := bufio.NewReader(conn)
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	say("220 sink.e2e.test ESMTP")
	var to []string
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			say("250-sink.e2e.test")
			say("250-8BITMIME")
			say("250-SMTPUTF8")
			say("250 PIPELINING")
		case strings.HasPrefix(cmd, "HELO"):
			say("250 sink.e2e.test")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			to = nil
			say("250 OK")
		case strings.HasPrefix(cmd, "RCPT TO"):
			addr := strings.TrimSpace(line[len("RCPT TO:"):])
			addr = strings.Trim(strings.Fields(addr)[0], "<>")
			to = append(to, strings.ToLower(addr))
			say("250 OK")
		case cmd == "DATA":
			say("354 go ahead")
			for {
				l, err := rd.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
			}
			s.mu.Lock()
			s.rcpt = append(s.rcpt, to)
			s.mu.Unlock()
			say("250 OK queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 OK")
		}
	}
}

func (s *smtpSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.rcpt)
}

// onlyTo fails unless every recorded message went to addr only.
func (s *smtpSink) onlyTo(t *testing.T, addr string) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rc := range s.rcpt {
		if len(rc) != 1 || rc[0] != addr {
			t.Fatalf("system mail to %v (only %s allowed)", rc, addr)
		}
	}
}
