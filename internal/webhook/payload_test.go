package webhook

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/events"
)

func payloadEvent() events.Event {
	return events.Event{
		ID:        "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Type:      events.MessageReceived,
		EBAccount: "acc_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Data: json.RawMessage(`{
			"mailbox":{"path":"INBOX"},
			"text":{"plain":"ab€cd","html":"<b>hello</b>"},
			"headers":{"Subject":"Hi","X-Test":"yes"},
			"attachments":[{"filename":"a.txt"}],
			"webSafeHtml":"safe",
			"calendarEvents":[{"uid":"1"}]
		}`),
	}
}

func TestEndpointMatchesFilters(t *testing.T) {
	event := payloadEvent()
	endpoint := &Endpoint{
		Enabled: true,
		Events:  []string{events.MessageReceived},
		Filter: EndpointFilter{
			AccountIDs:   []string{event.EBAccount},
			MailboxPaths: []string{"INBOX"},
		},
	}
	if !endpointMatches(endpoint, event) {
		t.Fatal("expected match")
	}
	for _, mutate := range []func(*Endpoint, *events.Event){
		func(e *Endpoint, _ *events.Event) { e.Enabled = false },
		func(e *Endpoint, _ *events.Event) { e.Events = []string{events.MessageUpdated} },
		func(e *Endpoint, _ *events.Event) { e.Filter.AccountIDs = []string{"acc_other"} },
		func(e *Endpoint, _ *events.Event) { e.Filter.MailboxPaths = []string{"Spam"} },
		func(_ *Endpoint, event *events.Event) { event.Type = events.EndpointTest },
		func(_ *Endpoint, event *events.Event) { event.Data = json.RawMessage(`{`) },
	} {
		gotEndpoint, gotEvent := *endpoint, event
		mutate(&gotEndpoint, &gotEvent)
		if endpointMatches(&gotEndpoint, gotEvent) {
			t.Fatalf("unexpected match: %+v %+v", gotEndpoint, gotEvent)
		}
	}
	endpoint.Events = []string{"*"}
	endpoint.Filter = EndpointFilter{}
	if !endpointMatches(endpoint, event) {
		t.Fatal("wildcard match")
	}
	if endpointMatches(nil, event) {
		t.Fatal("nil match")
	}
}

func TestBuildBodyTrimsPayloadWithoutSplittingUTF8(t *testing.T) {
	body, err := buildBody(payloadEvent(), nil, false, PayloadOptions{
		Text:        "plain",
		TextMax:     4,
		Headers:     []string{"subject"},
		WebSafeHTML: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	text := data["text"].(map[string]any)
	if text["plain"] != "ab" || text["truncated"] != true || text["html"] != nil {
		t.Fatalf("text = %+v", text)
	}
	headers := data["headers"].(map[string]any)
	if len(headers) != 1 || headers["Subject"] != "Hi" {
		t.Fatalf("headers = %+v", headers)
	}
	for _, key := range []string{"attachments", "webSafeHtml", "calendarEvents"} {
		if _, ok := data[key]; ok {
			t.Errorf("%s not trimmed", key)
		}
	}
	mapped, err := buildBody(payloadEvent(), map[string]any{"mapped": true}, true, PayloadOptions{})
	if err != nil || string(mapped) != `{"mapped":true}` {
		t.Fatalf("mapped = %s, %v", mapped, err)
	}
}

func TestPayloadOverrideAndCustomHeaders(t *testing.T) {
	base := PayloadOptions{Text: "none", TextMax: 10}
	got := payloadOverride(base, map[string]any{
		"text":           "both",
		"textMax":        20,
		"headers":        []any{"*"},
		"attachments":    true,
		"attachmentMax":  3,
		"webSafeHtml":    true,
		"calendarEvents": true,
	})
	if got.Text != "both" || got.TextMax != 20 || len(got.Headers) != 1 ||
		!got.Attachments || got.AttachmentMax != 3 || !got.WebSafeHTML || !got.CalendarEvents {
		t.Fatalf("override = %+v", got)
	}
	if !reflect.DeepEqual(payloadOverride(base, nil), base) ||
		!reflect.DeepEqual(payloadOverride(base, map[string]any{"bad": make(chan int)}), base) {
		t.Fatal("invalid override changed base")
	}
	headers := http.Header{}
	if err := addCustomHeaders(headers, map[string]string{
		"X-Custom":          "yes",
		"Webhook-Signature": "blocked",
	}); err != nil {
		t.Fatal(err)
	}
	if headers.Get("X-Custom") != "yes" || headers.Get("Webhook-Signature") != "" {
		t.Fatalf("custom headers = %v", headers)
	}
	for _, input := range []map[string]string{{"": "x"}, {"X-Bad": "x\r\ny"}} {
		if addCustomHeaders(http.Header{}, input) == nil {
			t.Fatalf("accepted headers %+v", input)
		}
	}
	if validPrefix("abc", 5) != "abc" || !strings.Contains(string(mustBody(t, PayloadOptions{Headers: []string{"*"}})), "X-Test") {
		t.Fatal("payload helpers")
	}
	htmlOnly := string(mustBody(t, PayloadOptions{Text: "html", TextMax: 100}))
	both := string(mustBody(t, PayloadOptions{
		Text: "both", TextMax: 100, Attachments: true, WebSafeHTML: true, CalendarEvents: true,
	}))
	if strings.Contains(htmlOnly, `"plain"`) || !strings.Contains(htmlOnly, `"html"`) ||
		!strings.Contains(both, `"plain"`) || !strings.Contains(both, `"attachments"`) ||
		!strings.Contains(both, `"webSafeHtml"`) || !strings.Contains(both, `"calendarEvents"`) {
		t.Fatal("payload modes")
	}
	if _, err := buildBody(payloadEvent(), make(chan int), true, PayloadOptions{}); err == nil {
		t.Fatal("non-JSON mapping accepted")
	}
	badData := map[string]any{"text": "bad", "headers": "bad"}
	trimText(badData, 1, true, true)
	trimHeaders(badData, []string{"subject"})
	if _, ok := badData["headers"]; ok {
		t.Fatal("invalid headers retained")
	}
}

func mustBody(t *testing.T, payload PayloadOptions) []byte {
	t.Helper()
	body, err := buildBody(payloadEvent(), nil, false, payload)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
