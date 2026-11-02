package rules

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// helperEvent is a message.received the way sync emits it (JSON-decoded), with every field the helpers read.
func helperEvent() map[string]any {
	return map[string]any{
		"type": "message.received", "time": "2026-10-27T10:30:00Z", // a Tuesday, 11:30 in Rome
		"data": map[string]any{
			"mailbox": map[string]any{"path": "Projects/Acme", "specialUse": `\Archive`},
			"labels":  []any{"Clients", `\Important`},
			"flags":   []any{`\Seen`},
			"subject": "Re: Invoice 42 for ACME",
			"from":    map[string]any{"name": "Ann", "address": "Ann@Partner.com"},
			"to":      []any{map[string]any{"address": "desk@example.com"}, map[string]any{"address": "b@acme.it"}},
			"cc":      []any{map[string]any{"address": "cc@other.org"}},
			"replyTo": []any{map[string]any{"address": "reply@partner.com"}},
			"text":    map[string]any{"plain": "", "html": "<p>Please pay <b>invoice</b> #42</p><script>x()</script>"},
			"attachments": []any{
				map[string]any{"filename": "inv-42.PDF", "contentType": "Application/PDF", "size": 2048.0, "contentId": "", "inline": false},
				map[string]any{"filename": "logo.png", "contentType": "image/png", "size": 10.0, "contentId": "c1", "inline": true},
			},
			"headers": map[string]any{
				"in-reply-to": []any{"<a@x>"}, "auto-submitted": []any{"auto-replied"}, "x-ricevuta": []any{"accettazione"},
			},
			"spamScore": "3.5",
		},
	}
}

func TestEveryHelperThroughCEL(t *testing.T) {
	e, err := NewEngine(EngineOptions{TextMax: 1 << 10})
	if err != nil {
		t.Fatal(err)
	}
	lists := map[string]map[string]struct{}{"vip": {"partner.com": {}}, "Words": {"urgent": {}}}
	cases := map[string]bool{
		`!isInbox()`:                true,
		`inFolder("projects/acme")`: true,
		`inFolder("\\Archive") && inFolder("archive")`: true,
		`inFolder("Clients")`:                          true, // a label is a folder for Gmail users
		`!inFolder("Sent")`:                            true,
		`hasLabel("clients") && hasLabel("\\Important") && !hasLabel("\\important")`: true,
		`"Clients" in labels() && "\\Seen" in labels()`:                              true,
		`text() == "Please pay invoice #42"`:                                         true,
		`textContains("INVOICE") && textMatches("#[0-9]+")`:                          true,
		`!textMatches("(")`: true, // invalid RE2 → false, never an error
		`subject().startsWith("Re:") && subjectContains("acme") && subjectMatches("[0-9]{2}")`: true,
		`!subjectMatches("[")`:                         true,
		`hasAttachments() && size(attachments()) == 2`: true,
		`attachments()[0].filename == "inv-42.PDF" && attachments()[1].inline == true`:                                     true,
		`hasAttachment("*.pdf") && hasAttachment("/^inv-[0-9]+/") && !hasAttachment("*.zip")`:                              true,
		`!hasAttachment("/(/") && !hasAttachment("")`:                                                                      true,
		`attachmentTypes() == ["application/pdf", "image/png"]`:                                                            true,
		`from() == "Ann@Partner.com" && fromDomain() == "partner.com"`:                                                     true,
		`toAddresses() == ["desk@example.com", "b@acme.it"] && ccAddresses() == ["cc@other.org"]`:                          true,
		`inDomains(["ACME.it"]) && inDomains(["other.org"]) && !inDomains(["nowhere.net"])`:                                true,
		`replyTo() == "reply@partner.com"`:                                                                                 true,
		`isReply() && !isForward() && isAutoReply()`:                                                                       true,
		`isPecReceipt() && spamScore() == 3.5`:                                                                             true,
		`hourOf(localTime("Europe/Rome")) == 11 && weekdayOf(localTime("Europe/Rome")) == 2`:                               true,
		`hourOf(localTime("No/Such_Zone")) == 10`:                                                                          true, // unknown zone → UTC
		`businessHours("Europe/Rome", "09:00-18:00", "Mon-Fri")`:                                                           true,
		`!businessHours("Europe/Rome", "12:00-18:00", "Mon-Fri")`:                                                          true,
		`!businessHours("Europe/Rome", "09:00-18:00", "Sat,Sun")`:                                                          true,
		`!businessHours("Bad/Zone", "09:00-18:00", "Mon-Fri") && !businessHours("UTC", "nope", "Mon-Fri")`:                 true,
		`inList("vip", fromDomain()) && inList("VIP", "Partner.com") && inList("Words", "URGENT") && !inList("none", "x")`: true,
		`now == timestamp("2026-10-27T10:30:00Z")`:                                                                         true,
		`domain(data.from) == "partner.com" && addresses(data.to) == ["desk@example.com", "b@acme.it"]`:                    true,
	}
	for expr, want := range cases {
		p, err := e.Compile(expr, "")
		if err != nil {
			t.Errorf("compile %s: %v", expr, err)
			continue
		}
		got, _, _, err := e.EvalWithLists(context.Background(), p, helperEvent(), nil, nil, lists)
		if err != nil || got != want {
			t.Errorf("%s = %v (err %v), want %v", expr, got, err, want)
		}
	}
}

func TestMapHelpersThroughCEL(t *testing.T) {
	e, err := NewEngine(EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(`true`, `{
  "m": merge({"a": 1, "b": {"x": true}}, {"b": 2, "c": null}),
  "p": pick(data, ["subject", "missing"]),
  "o": omit({"a": 1, "b": 2, "c": [1, "two", 3.5]}, ["a"]),
  "t": now,
  "d": duration("90s")
}`)
	if err != nil {
		t.Fatal(err)
	}
	_, mapped, _, err := e.EvalWithLists(context.Background(), p, helperEvent(), nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"m": map[string]any{"a": 1.0, "b": 2.0, "c": nil},
		"p": map[string]any{"subject": "Re: Invoice 42 for ACME"},
		"o": map[string]any{"b": 2.0, "c": []any{1.0, "two", 3.5}},
		"t": "2026-10-27T10:30:00Z",
		"d": "1m30s",
	}
	if !reflect.DeepEqual(mapped, want) {
		t.Fatalf("mapped = %#v", mapped)
	}
	// Bytes and non-string keys are not JSON.
	for _, bad := range []string{`{"b": b"raw"}`, `{1: "x"}`, `b"raw"`} {
		p, err := e.Compile(`true`, bad)
		if err != nil {
			continue // rejected at compile time is fine too
		}
		if _, _, _, err := e.EvalWithLists(context.Background(), p, helperEvent(), nil, nil, nil); err == nil {
			t.Errorf("map %s accepted", bad)
		}
	}
}

func TestTextHelperCapsAndPrefersPlain(t *testing.T) {
	e, err := NewEngine(EngineOptions{TextMax: 8})
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(`true`, `{"t": text()}`)
	if err != nil {
		t.Fatal(err)
	}
	ev := map[string]any{"type": "message.received", "data": map[string]any{
		"text": map[string]any{"plain": "àèìòù and more", "html": "<b>ignored</b>"}}}
	_, mapped, _, err := e.EvalWithLists(context.Background(), p, ev, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := mapped.(map[string]any)["t"].(string)
	if len(got) > 8 || !strings.HasPrefix("àèìòù and more", got) || strings.ContainsRune(got, '�') {
		t.Fatalf("capped text = %q", got)
	}
}

func TestBoolHelperConditionValues(t *testing.T) {
	for _, c := range []struct {
		op    string
		value any
		want  string
		fails bool
	}{
		{"eq", nil, "isInbox()", false}, {"eq", true, "isInbox()", false}, {"eq", false, "!isInbox()", false},
		{"neq", true, "!isInbox()", false}, {"neq", false, "isInbox()", false},
		{"exists", nil, "isInbox()", false}, {"notExists", nil, "!isInbox()", false},
		{"eq", "yes", "", true}, {"gt", true, "", true},
	} {
		got, err := boolHelperCondition("isInbox()", c.op, c.value)
		if (err != nil) != c.fails || got != c.want {
			t.Errorf("%s %v = %q %v", c.op, c.value, got, err)
		}
	}
}
