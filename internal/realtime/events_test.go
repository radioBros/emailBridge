package realtime

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
)

func TestTopicsFor(t *testing.T) {
	if got := topicsFor(events.AccountStateChanged); !slices.Equal(got, []string{"account.events", "accounts.state"}) {
		t.Fatalf("%v", got)
	}
	if got := topicsFor(events.MessageSent); !slices.Equal(got, []string{"account.events", "outbox.updates"}) {
		t.Fatalf("%v", got)
	}
	if got := topicsFor(events.MessageReceived); !slices.Equal(got, []string{"account.events"}) {
		t.Fatalf("%v", got)
	}
	if got := topicsFor(events.EndpointTest); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestEventsReachSockets(t *testing.T) {
	e := setup(t)
	c, _, err := e.dial(t, "admin", e.srv.URL) //nolint:bodyclose // closed in dial
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, map[string]any{"op": "sub", "id": "acc", "topic": "account.events", "params": map[string]string{"accountId": "acc_1"}})
	recv(t, c)
	send(t, c, map[string]any{"op": "sub", "id": "st", "topic": "accounts.state"})
	recv(t, c)
	time.Sleep(200 * time.Millisecond)
	publish := func(org, acc, typ string) {
		body, _ := json.Marshal(events.Event{SpecVersion: "1.0", ID: "evt_x", Type: typ, EBOrg: org, EBAccount: acc, Data: json.RawMessage(`{}`)})
		if _, err := e.b.Publish(context.Background(), bus.EventSubject(0, org, typ), body, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	publish("org_2", "acc_1", events.AccountStateChanged) // another organisation: never delivered
	publish("org_1", "acc_2", events.MessageReceived)     // other account: filtered
	publish("org_1", "acc_1", events.AccountStateChanged)
	got := map[string]string{}
	for len(got) < 2 {
		m := recv(t, c)
		got[m["id"].(string)] = m["type"].(string)
	}
	if got["acc"] != events.AccountStateChanged || got["st"] != events.AccountStateChanged {
		t.Fatalf("deliveries %v", got)
	}
	publish("org_1", "acc_1", events.MessageReceived)
	if m := recv(t, c); m["id"] != "acc" || m["type"] != events.MessageReceived {
		t.Fatalf("next delivery %v", m)
	}
}
