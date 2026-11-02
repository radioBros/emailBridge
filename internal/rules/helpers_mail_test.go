package rules

import (
	"testing"
	"time"

	"cel.dev/cel-go/common/types"
)

func TestIsInbox_IMAPGmailGraph(t *testing.T) {
	cases := []struct {
		name string
		data map[string]any
		want bool
	}{
		{"imap", map[string]any{"mailbox": map[string]any{"path": "INBOX"}}, true},
		{"imap lower", map[string]any{"mailbox": map[string]any{"path": "Inbox"}}, true},
		{"gmail label", map[string]any{"path": "[Gmail]/All Mail", "labels": []string{`\Inbox`, "UNREAD"}}, true},
		{"graph", map[string]any{"mailbox": map[string]any{"wellKnownName": "inbox"}}, true},
		{"specialUse", map[string]any{"mailbox": map[string]any{"specialUse": `\Inbox`}}, true},
		{"sent", map[string]any{"mailbox": map[string]any{"path": "Sent"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := computeIsInbox(tc.data); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestTextAndAttachmentsHelpers(t *testing.T) {
	setEvalBag(map[string]any{
		"text": map[string]any{"html": "<p>Hello <b>World</b></p>"},
		"attachments": []any{
			map[string]any{"filename": "inv.pdf", "contentType": "application/pdf", "size": 12},
		},
		"subject": "Re: invoice",
		"from":    map[string]any{"address": "a@Example.COM"},
		"headers": map[string]any{"In-Reply-To": []any{"<x@y>"}},
	}, nil, time.Now().UTC(), 0)

	if !bool(textContainsFn(types.String("hello")).(types.Bool)) {
		t.Fatal("textContains")
	}
	if !bool(hasAttachmentsFn().(types.Bool)) {
		t.Fatal("hasAttachments")
	}
	if !bool(hasAttachmentFn(types.String("*.pdf")).(types.Bool)) {
		t.Fatal("hasAttachment glob")
	}
	if string(fromDomainFn().(types.String)) != "example.com" {
		t.Fatal(fromDomainFn())
	}
	if !bool(isReplyFn().(types.Bool)) {
		t.Fatal("isReply")
	}
}

func TestBusinessHoursAndInList(t *testing.T) {
	// Monday 2026-10-26 10:00 UTC
	et := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	setEvalBag(map[string]any{}, map[string]map[string]struct{}{
		"vip": {"example.com": {}},
	}, et, 0)
	ok := businessHoursFn(types.String("UTC"), types.String("09:00-18:00"), types.String("Mon-Fri"))
	if !bool(ok.(types.Bool)) {
		t.Fatal("businessHours")
	}
	if !bool(inListFn(types.String("vip"), types.String("Example.COM")).(types.Bool)) {
		t.Fatal("inList")
	}
}

func TestMergePickOmit(t *testing.T) {
	a := types.DefaultTypeAdapter.NativeToValue(map[string]any{"a": 1, "b": 2})
	b := types.DefaultTypeAdapter.NativeToValue(map[string]any{"b": 3, "c": 4})
	m := mergeFn(a, b).Value().(map[string]any)
	if m["a"] != 1 || m["b"] != 3 || m["c"] != 4 {
		t.Fatalf("%v", m)
	}
	keys := types.DefaultTypeAdapter.NativeToValue([]string{"a", "c"})
	p := pickFn(mergeFn(a, b), keys).Value().(map[string]any)
	if len(p) != 2 || p["a"] != 1 {
		t.Fatalf("%v", p)
	}
	o := omitFn(mergeFn(a, b), keys).Value().(map[string]any)
	if _, ok := o["b"]; !ok || len(o) != 1 {
		t.Fatalf("%v", o)
	}
}

func TestNeedsFromExpr(t *testing.T) {
	h := NeedsFromExpr(`type == "message.received" && textContains("x") && hasAttachment("*.pdf")`, `merge(event, {"t": text()})`)
	if !h.Text || !h.Attachments {
		t.Fatalf("%+v", h)
	}
}

func TestBuilderNestedAndIsInbox(t *testing.T) {
	cel, err := BuilderToCEL(Builder{
		Match: MatchAll,
		Conditions: []BuilderCondition{
			{Field: "type", Op: "in", Value: []string{"message.received"}},
			{Match: MatchAny, Conditions: []BuilderCondition{
				{Field: "isInbox", Op: "eq"},
				{Field: "label", Op: "eq", Value: `\Inbox`},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `(type in ["message.received"]) && ((isInbox()) || (hasLabel("\\Inbox")))`
	if cel != want {
		t.Fatalf("got %q want %q", cel, want)
	}
}

func TestEngineEvalIsInbox(t *testing.T) {
	eng, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	prg, err := eng.Compile(`type == "message.received" && isInbox()`, "")
	if err != nil {
		t.Fatal(err)
	}
	matched, _, _, err := eng.Eval(t.Context(), prg, map[string]any{
		"type": "message.received",
		"data": map[string]any{"labels": []string{`\Inbox`}},
	}, map[string]any{"externalId": "testaccount"}, nil)
	if err != nil || !matched {
		t.Fatalf("matched=%v err=%v", matched, err)
	}
}

func TestTraceTerms(t *testing.T) {
	eng, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	tr := eng.BuildTrace(t.Context(), `type == "message.received" && isInbox()`, map[string]any{
		"type": "message.received",
		"data": map[string]any{"mailbox": map[string]any{"path": "Sent"}},
	}, nil, nil, nil)
	if len(tr) != 2 || !tr[0].Result || tr[1].Result {
		t.Fatalf("%+v", tr)
	}
}
