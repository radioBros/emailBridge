package rules

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func testEngine(t *testing.T, o EngineOptions) *Engine {
	t.Helper()
	e, err := NewEngine(o)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func sampleEvent() map[string]any {
	data := map[string]any{
		"messageId":       "msg_1",
		"messageIdHeader": "<m@example.com>",
		"subject":         "POSTA CERTIFICATA: ricevuta",
		"from":            map[string]any{"name": "Alice", "address": "alice@pec.example.it"},
		"to": []any{
			map[string]any{"address": "bob@example.com"},
			map[string]any{"address": "carol@other.com"},
		},
		"mailbox": map[string]any{"path": "INBOX", "specialUse": "\\Inbox"},
		"attachments": []any{
			map[string]any{"filename": "a.pdf"},
			map[string]any{"filename": "b.txt"},
		},
		"headers": map[string][]string{
			"x-ricevuta":               {"accettazione"},
			"x-riferimento-message-id": {"<ref@example.it>"},
		},
		"text": map[string]any{"plain": strings.Repeat("lorem ipsum dolor sit amet ", 100)},
	}
	return map[string]any{
		"specversion": "1.0",
		"id":          "evt_1",
		"type":        "message.received",
		"data":        data,
	}
}

func TestS8ExampleExpressions(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	ev := sampleEvent()
	account := map[string]any{"id": "acc_1", "email": "bob@example.com", "name": "Bob", "externalId": "ext"}
	env := map[string]string{"webhookKey": "secret-key"}

	cases := []struct {
		name, filter, mapExpr string
		wantMatch             bool
	}{
		{"ex_pec_filter", `type == "message.received" && hasHeader("x-ricevuta")`, "", true},
		{"ex_slim_map", `true`, `{"messageId": data.messageIdHeader, "receipt": header("x-ricevuta"), "ref": header("x-riferimento-message-id")}`, true},
		{"ex_legacy_env", `true`, `{"key": env.webhookKey, "body": event}`, true},
		{"f_type_received", `type == "message.received"`, "", true},
		{"f_type_sent", `type == "message.sent"`, "", false},
		{"f_has_ricevuta", `hasHeader("x-ricevuta")`, "", true},
		{"f_from_domain", `domain(data.from) == "pec.example.it"`, "", true},
		{"f_subject_contains", `data.subject.contains("CERTIFICATA")`, "", true},
		{"f_subject_matches", `data.subject.matches("(?i)ricevuta")`, "", true},
		{"f_to_exists_domain", `data.to.exists(r, domain(r) == "example.com")`, "", true},
		{"f_account_email", `account.email == "bob@example.com"`, "", true},
		{"f_mailbox_inbox", `data.mailbox.specialUse == "\\Inbox"`, "", true},
		{"f_header_values", `headerValues("x-ricevuta").exists(v, v == "accettazione")`, "", true},
		{"m_slim_ids", `true`, `{"id": data.messageId, "mid": data.messageIdHeader, "subject": data.subject}`, true},
		{"m_addresses", `true`, `{"from": domain(data.from), "to": addresses(data.to)}`, true},
		{"m_attach_names", `true`, `{"files": data.attachments.map(a, a.filename)}`, true},
		{"m_filter_to", `true`, `{"to": data.to.filter(r, domain(r) == "example.com").map(r, r.address)}`, true},
		{"m_receipt", `true`, `{"receipt": header("x-ricevuta"), "ref": header("x-riferimento-message-id"), "nAttach": data.attachments.size()}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := e.Compile(tc.filter, tc.mapExpr)
			if err != nil {
				t.Fatal(err)
			}
			matched, mapped, cost, err := e.Eval(context.Background(), p, ev, account, env)
			if err != nil {
				t.Fatal(err)
			}
			if matched != tc.wantMatch {
				t.Fatalf("matched=%v want %v (cost=%d)", matched, tc.wantMatch, cost)
			}
			if tc.mapExpr != "" && matched && mapped == nil {
				t.Fatal("expected mapped output")
			}
		})
	}
}

func TestCompileFilterMustBeBool(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	_, err := e.Compile(`"hello"`, "")
	if err == nil || errs.CodeOf(err) != errs.CodeRouteCompileError {
		t.Fatalf("got %v", err)
	}
}

func TestCompileDeepNestRejected(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	deep := deepNest(32)
	_, err := e.Compile("true", deep)
	if err == nil || errs.CodeOf(err) != errs.CodeRouteCompileError {
		t.Fatalf("got %v", err)
	}
}

func TestCompileEmptyFilter(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	_, err := e.Compile("  ", "")
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCostLimit(t *testing.T) {
	e := testEngine(t, EngineOptions{CostLimit: 500})
	p, err := e.Compile("true", `lists.range(500).map(i, i * i).filter(i, i % 2 == 0).size()`)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := e.Eval(context.Background(), p, sampleEvent(), nil, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || errs.CodeOf(err) != errs.CodeRouteCostExceeded {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cost limit did not fire in time")
	}
}

func TestTimeLimit(t *testing.T) {
	e := testEngine(t, EngineOptions{CostLimit: 50_000_000, TimeLimit: time.Millisecond})
	p, err := e.Compile("true", `lists.range(80).map(i, lists.range(80).map(j, i + j)).size()`)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := e.Eval(context.Background(), p, sampleEvent(), nil, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected timeout or cost error")
		}
		code := errs.CodeOf(err)
		if code != errs.CodeRouteTimeout && code != errs.CodeRouteCostExceeded {
			t.Fatalf("got %v code %s", err, code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("time/cost limit did not fire")
	}
}

func TestStringDoubleCost(t *testing.T) {
	e := testEngine(t, EngineOptions{CostLimit: 5_000, TimeLimit: 50 * time.Millisecond})
	p, err := e.Compile("true", `lists.range(2000).map(i, "xy").join("").size()`)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := e.Eval(context.Background(), p, sampleEvent(), nil, nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected limit error")
		}
		code := errs.CodeOf(err)
		if code != errs.CodeRouteCostExceeded && code != errs.CodeRouteTimeout {
			t.Fatalf("got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("limit did not fire")
	}
}

func TestBytesMapRefused(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	// bytes literal via encoders extension: decode base64 to bytes
	p, err := e.Compile("true", `base64.decode("YWI=")`) // "ab"
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = e.Eval(context.Background(), p, sampleEvent(), nil, nil)
	if err == nil || errs.CodeOf(err) != errs.CodeRouteEvalError {
		t.Fatalf("got %v", err)
	}
}

func TestCompileErrorHasPosition(t *testing.T) {
	e := testEngine(t, EngineOptions{})
	_, err := e.Compile("type == ", "")
	var e2 *errs.E
	if err == nil || !errors.As(err, &e2) || e2.Safe["path"] != "filter" {
		t.Fatalf("%+v", err)
	}
}

func deepNest(depth int) string {
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString(`{"k":`)
	}
	b.WriteString(`1`)
	for i := 0; i < depth; i++ {
		b.WriteString(`}`)
	}
	return b.String()
}

func TestEnvCostAndTimeFromEnv(t *testing.T) {
	t.Setenv("EB_RULES_COST_LIMIT", "12345")
	t.Setenv("EB_RULES_TIME_LIMIT", "25ms")
	if envCostLimit() != 12345 {
		t.Fatal(envCostLimit())
	}
	if envTimeLimit() != 25*time.Millisecond {
		t.Fatal(envTimeLimit())
	}
	t.Setenv("EB_RULES_TIME_LIMIT", "30")
	if envTimeLimit() != 30*time.Millisecond {
		t.Fatal(envTimeLimit())
	}
}
