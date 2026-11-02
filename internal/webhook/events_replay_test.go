package webhook

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
)

func TestTypesMatchAndReplayFilters(t *testing.T) {
	if !typesMatch([]string{"message.received"}, "message.received") {
		t.Fatal("exact")
	}
	if !typesMatch([]string{"message.*"}, "message.updated") {
		t.Fatal("family")
	}
	if !typesMatch([]string{"a, message.received"}, "message.received") {
		t.Fatal("comma")
	}
	if typesMatch([]string{"account.*"}, "message.received") {
		t.Fatal("no match")
	}

	ep := &Endpoint{
		Events: []string{events.MessageReceived},
		Filter: EndpointFilter{AccountIDs: []string{"acc_a"}},
	}
	ev := events.Event{
		ID: "evt_1", Type: events.MessageReceived, EBAccount: "acc_a",
		Data: json.RawMessage(`{"mailbox":{"path":"INBOX"}}`),
	}
	req := EventsReplayRequest{}
	if !eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("should pass")
	}
	ev.EBAccount = "acc_b"
	if eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("account filter")
	}
	ev.EBAccount = "acc_a"
	ev.Type = events.MessageUpdated
	if eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("events allow-list")
	}
	ev.Type = events.MessageReceived
	req.IgnoreEndpointFilter = true
	ev.EBAccount = "acc_other"
	if !eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("ignore filter")
	}
	req.IgnoreEndpointFilter = false
	req.Types = []string{"message.updated"}
	if eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("req types")
	}
	ev.Type = events.EndpointTest
	req.Types = nil
	req.IgnoreEndpointFilter = true
	if eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("endpoint.test")
	}

	ep.Filter.MailboxPaths = []string{"Sent"}
	ev.Type = events.MessageReceived
	req.IgnoreEndpointFilter = false
	ev.EBAccount = "acc_a"
	if eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("mailbox")
	}
	ep.Filter.MailboxPaths = []string{"INBOX"}
	if !eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("mailbox ok")
	}
	ev.Data = json.RawMessage(`{`)
	if eventPassesReplayFilter(ep, ev, req) {
		t.Fatal("bad data")
	}
}

func TestReplayCatchUpFilters(t *testing.T) {
	ep := &Endpoint{
		Events: []string{events.MessageReceived, events.MessageUpdated},
		Filter: EndpointFilter{AccountIDs: []string{"acc_1"}},
	}
	types, acc := replayCatchUpFilters(ep, EventsReplayRequest{})
	if len(types) != 2 || acc != "acc_1" {
		t.Fatalf("got %v %q", types, acc)
	}
	types, acc = replayCatchUpFilters(ep, EventsReplayRequest{
		Types: []string{"message.received"}, IgnoreEndpointFilter: true,
	})
	if len(types) != 1 || acc != "" {
		t.Fatalf("ignore: %v %q", types, acc)
	}
	ep.Events = []string{"*"}
	types, _ = replayCatchUpFilters(ep, EventsReplayRequest{})
	if len(types) != 0 {
		t.Fatalf("wildcard endpoint events should not push types: %v", types)
	}
	ep.Filter.AccountIDs = []string{"acc_1", "acc_2"}
	_, acc = replayCatchUpFilters(ep, EventsReplayRequest{})
	if acc != "" {
		t.Fatal("multi account")
	}
}

func TestEventsRetentionHelper(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	s := &Service{Now: func() time.Time { return now }}
	if s.eventsRetention() != 7*24*time.Hour {
		t.Fatal("default retention")
	}
	s.Events = &events.Writer{Retention: func() time.Duration { return 48 * time.Hour }}
	if s.eventsRetention() != 48*time.Hour {
		t.Fatal("custom")
	}
	s.Events.Retention = func() time.Duration { return 0 }
	if s.eventsRetention() != 7*24*time.Hour {
		t.Fatal("zero falls back")
	}
}
