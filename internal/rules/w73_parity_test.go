package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
)

// The LegacyApp webhooks, translated from their EmailEngine filter/map functions (W73 follow-up). The key
// is a secret in settings.env; tests use a fake value only.
const (
	keyedInboundFilter = `type == "message.received" && isInbox()`
	keyedInboundMap    = `{
  "key": env.webhookKey,
  "body": {
    "id": data.messageId,
    "date": event.time,
    "account": account.externalId != "" ? account.externalId : account.id,
    "event": "messageNew",
    "headers": data.headers,
    "emailid": data.messageIdHeader,
    "subject": data.subject
  }
}`
	legacyappOutboundFilter = `type in ["message.sent", "message.failed", "message.delivery_attempt_failed"]`
	legacyappOutboundMap    = `{"key": env.webhookKey, "body": event}`
	fakeLegacyAppKey        = "fake-legacyapp-key-for-tests"
)

// jsonEvent returns the event the way the router sees it: a JSON round trip of the CloudEvents envelope.
func jsonEvent(t *testing.T, typ, path string, labels []string) map[string]any {
	t.Helper()
	env := map[string]any{
		"specversion": "1.0", "id": "evt_parity", "type": typ, "time": "2026-10-27T09:30:00Z",
		"source": "/organizations/org_a/accounts/acc_1", "eborg": "org_a", "ebaccount": "acc_1",
		"data": map[string]any{
			"accountId": "acc_1", "externalId": nil, "messageId": "msg_01parity",
			"mailbox":         map[string]any{"mailboxId": "mbx_1", "path": path},
			"messageIdHeader": "<abc@example.com>", "subject": "Verbale 42", "labels": labels,
			"headers": map[string][]string{
				"subject": {"Verbale 42"}, "message-id": {"<abc@example.com>"}, "received": {"by a", "by b"},
			},
		},
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestKeyedInboundParityGolden(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(keyedInboundFilter, keyedInboundMap)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"webhookKey": fakeLegacyAppKey}
	account := map[string]any{"id": "acc_1", "externalId": "studio-rossi", "email": "a@x.it", "name": "A"}

	want := map[string]any{
		"key": fakeLegacyAppKey,
		"body": map[string]any{
			"id": "msg_01parity", "date": "2026-10-27T09:30:00Z", "account": "studio-rossi",
			"event": "messageNew", "emailid": "<abc@example.com>", "subject": "Verbale 42",
			"headers": map[string]any{
				"subject": []any{"Verbale 42"}, "message-id": []any{"<abc@example.com>"},
				"received": []any{"by a", "by b"},
			},
		},
	}
	cases := []struct {
		name    string
		path    string
		labels  []string
		matched bool
	}{
		{"imap inbox", "INBOX", nil, true},
		{"imap inbox lower-case", "inbox", nil, true},
		{"gmail all mail with \\Inbox label", "[Gmail]/All Mail", []string{`\Inbox`}, true},
		{"gmail all mail with INBOX label id", "[Gmail]/All Mail", []string{"INBOX"}, true},
		{"gmail all mail archived", "[Gmail]/All Mail", []string{`\Important`}, false},
		{"other folder", "Archive", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			matched, mapped, _, err := e.EvalWithLists(context.Background(), p, jsonEvent(t, "message.received", c.path, c.labels), account, env, nil)
			if err != nil {
				t.Fatal(err)
			}
			if matched != c.matched {
				t.Fatalf("matched = %v, want %v", matched, c.matched)
			}
			if c.matched && !reflect.DeepEqual(mapped, want) {
				got, _ := json.Marshal(mapped)
				exp, _ := json.Marshal(want)
				t.Fatalf("mapped\n got %s\nwant %s", got, exp)
			}
		})
	}

	// No externalId → the ternary falls back to the account id (account.externalId is always a string).
	for _, acc := range []map[string]any{
		{"id": "acc_1"}, {"id": "acc_1", "externalId": nil}, nil,
	} {
		_, mapped, _, err := e.EvalWithLists(context.Background(), p, jsonEvent(t, "message.received", "INBOX", nil), acc, env, nil)
		if err != nil {
			t.Fatalf("account %v: %v", acc, err)
		}
		body := mapped.(map[string]any)["body"].(map[string]any)
		if body["account"] != "acc_1" {
			t.Fatalf("account %v → %v", acc, body["account"])
		}
	}
	// A non-message event never matches.
	if m, _, _, err := e.EvalWithLists(context.Background(), p, jsonEvent(t, "message.sent", "INBOX", nil), account, env, nil); err != nil || m {
		t.Fatalf("message.sent matched=%v err=%v", m, err)
	}
}

func TestLegacyAppOutboundParityGolden(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(legacyappOutboundFilter, legacyappOutboundMap)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"webhookKey": fakeLegacyAppKey}
	for _, typ := range []string{"message.sent", "message.failed", "message.delivery_attempt_failed", "message.received", "message.bounced"} {
		ev := jsonEvent(t, typ, "INBOX", nil)
		matched, mapped, _, err := e.EvalWithLists(context.Background(), p, ev, nil, env, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := typ != "message.received" && typ != "message.bounced"
		if matched != want {
			t.Fatalf("%s matched = %v", typ, matched)
		}
		if !want {
			continue
		}
		m := mapped.(map[string]any)
		if m["key"] != fakeLegacyAppKey || !reflect.DeepEqual(m["body"], ev) {
			t.Fatalf("%s mapped = %v", typ, m)
		}
	}
}

func TestNeedsFromExprDataHeaders(t *testing.T) {
	cases := []struct {
		filter, mapExpr string
		headers         []string
		text, att       bool
	}{
		{keyedInboundFilter, keyedInboundMap, []string{"*"}, false, false},
		{`true`, `{"h": data.headers}`, []string{"*"}, false, false},
		{`size(data.headers) > 0`, ``, []string{"*"}, false, false},
		{`data.headers["x-tag"][0] == "a"`, ``, []string{"x-tag"}, false, false},
		{`data.headers['x-a'] == ["1"] && has(data.headers.priority)`, ``, []string{"priority", "x-a"}, false, false},
		{`true`, `{"t": data.text.plain, "a": data.attachments}`, nil, true, true},
		{`data.headersFoo == 1`, ``, nil, false, false},
		{`type == "message.received"`, `{"body": event}`, nil, false, false},
		{`header("X-Prio") == "1" && hasHeader('list-id')`, `{"v": headerValues("received")}`, []string{"list-id", "received", "x-prio"}, false, false},
		{`header(data.subject) == ""`, ``, []string{"*"}, false, false},
	}
	for _, c := range cases {
		n := NeedsFromExpr(c.filter, c.mapExpr)
		got := append([]string(nil), n.Headers...)
		slices.Sort(got)
		if !slices.Equal(got, c.headers) || n.Text != c.text || n.Attachments != c.att {
			t.Errorf("NeedsFromExpr(%q, %q) = %+v", c.filter, c.mapExpr, n)
		}
	}
}

func TestNormalizeAccount(t *testing.T) {
	ext := "ext-1"
	got := normalizeAccount(map[string]any{"externalId": &ext, "custom": 1}, map[string]any{"ebaccount": "acc_9"}, map[string]any{})
	if got["id"] != "acc_9" || got["externalId"] != "ext-1" || got["email"] != "" || got["name"] != "" || got["custom"] != 1 {
		t.Fatalf("normalize = %v", got)
	}
	var nilExt *string
	got = normalizeAccount(map[string]any{"id": "acc_1", "externalId": nilExt}, map[string]any{}, map[string]any{"externalId": "from-data"})
	if got["externalId"] != "from-data" || got["id"] != "acc_1" {
		t.Fatalf("normalize fallback = %v", got)
	}
	got = normalizeAccount(nil, map[string]any{}, map[string]any{"accountId": "acc_2", "externalId": nil})
	if got["id"] != "acc_2" || got["externalId"] != "" {
		t.Fatalf("normalize data = %v", got)
	}
}

// TestConcurrentEvalsDoNotShareContext runs many evaluations of different organisations at once: each must
// see only its own data and named lists (the helpers read a per-evaluation bag; evalMu guards it).
func TestConcurrentEvalsDoNotShareContext(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(`inList("vip", fromDomain()) && textContains(data.subject)`, `{"from": from(), "subject": subject()}`)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errc := make(chan error, 64)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			domain := fmt.Sprintf("org%d.example", g)
			lists := map[string]map[string]struct{}{"vip": {domain: {}}}
			for i := range 200 {
				subject := fmt.Sprintf("s-%d-%d", g, i)
				ev := map[string]any{"type": "message.received", "data": map[string]any{
					"subject": subject, "from": map[string]any{"address": "x@" + domain},
					"text": map[string]any{"plain": "body " + subject},
				}}
				matched, mapped, _, err := e.EvalWithLists(context.Background(), p, ev, nil, nil, lists)
				if err != nil {
					errc <- err
					return
				}
				m, _ := mapped.(map[string]any)
				if !matched || m["subject"] != subject || m["from"] != "x@"+domain {
					errc <- fmt.Errorf("goroutine %d eval %d saw foreign context: matched=%v mapped=%v", g, i, matched, mapped)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Fatal(err)
	}
}

func TestStatsRecorderMemoryAndNilSafety(t *testing.T) {
	var nilRec *StatsRecorder
	nilRec.Observe("o", "route", "r", "e", true, "", "")
	nilRec.Flush(context.Background())
	nilRec.Run(context.Background())
	if nilRec.Collector() != nil {
		t.Fatal("nil collector")
	}
	s := NewStatsRecorder(nil)
	for i := range statsFlushThreshold + 1 {
		s.Observe("org_a", "endpoint", "whe_1", fmt.Sprintf("evt_%d", i), i%2 == 0, "", "")
	}
	s.Observe("org_a", "endpoint", "whe_1", "evt_err", false, "boom", "")
	s.mu.Lock()
	p := s.pending["org_a:endpoint:whe_1"]
	s.mu.Unlock()
	if p == nil || p.evaluated != statsFlushThreshold+2 || p.errors != 1 || p.lastError != "boom" || p.matched+p.skipped+p.errors != p.evaluated {
		t.Fatalf("pending = %+v", p)
	}
	select {
	case <-s.kick:
	default:
		t.Fatal("early flush not signalled")
	}
	s.Flush(context.Background()) // no DB: keeps the batch, no panic
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.Run(ctx) // returns at once on a cancelled context
}

// TestHelpersOnTheRealEventShape checks the trust / class helpers against message.received as sync emits it
// (authResults objects and lists, spam.score, bounce object, isAutoReply flag, pec.kind).
func TestHelpersOnTheRealEventShape(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ev := map[string]any{"type": "message.received", "data": map[string]any{
		"authResults": map[string]any{
			"spf":   map[string]any{"result": "pass", "domain": "ex.com"},
			"dkim":  []any{map[string]any{"result": "fail"}, map[string]any{"result": "pass"}},
			"dmarc": map[string]any{"result": "fail"},
		},
		"spam":        map[string]any{"score": 7.5, "action": "add header"},
		"bounce":      map[string]any{"kind": "dsn_bounce", "bounceType": "hard"},
		"isAutoReply": true,
		"pec":         map[string]any{"kind": "receipt", "type": "accettazione"},
	}}
	for expr, want := range map[string]bool{
		`spf() == "pass"`:                 true,
		`dkim() == "pass"`:                true,
		`dmarc() == "fail"`:               true,
		`spamScore() > 5.0`:               true,
		`isBounce()`:                      true,
		`isAutoReply()`:                   true,
		`isPecReceipt()`:                  true,
		`pecType() == "accettazione"`:     true,
		`spf() == "fail" || dkim() == ""`: false,
	} {
		p, err := e.Compile(expr, "")
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		got, _, _, err := e.EvalWithLists(context.Background(), p, ev, nil, nil, nil)
		if err != nil || got != want {
			t.Errorf("%s = %v (%v), want %v", expr, got, err, want)
		}
	}
	// message.bounced is a bounce whatever its data; a complaint is not.
	p, _ := e.Compile(`isBounce()`, "")
	if got, _, _, _ := e.EvalWithLists(context.Background(), p, map[string]any{"type": "message.bounced", "data": map[string]any{}}, nil, nil, nil); !got {
		t.Error("message.bounced is a bounce")
	}
	complaint := map[string]any{"type": "message.received", "data": map[string]any{"bounce": map[string]any{"kind": "complaint"}}}
	if got, _, _, _ := e.EvalWithLists(context.Background(), p, complaint, nil, nil, nil); got {
		t.Error("a complaint is not a bounce")
	}
	dkimFirst := map[string]any{"type": "message.received", "data": map[string]any{
		"authResults": map[string]any{"dkim": []any{map[string]any{"result": "neutral"}}, "spf": "softfail"}}}
	p2, _ := e.Compile(`dkim() == "neutral" && spf() == "softfail"`, "")
	if got, _, _, err := e.EvalWithLists(context.Background(), p2, dkimFirst, nil, nil, nil); !got || err != nil {
		t.Errorf("dkim first / spf string: %v %v", got, err)
	}
}
