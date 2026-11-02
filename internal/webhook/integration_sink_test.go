package webhook

import (
	"context"
	"errors"
	"testing"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/sinks"
)

func TestSendIntegrationAction(t *testing.T) {
	var gotID string
	svc := &Service{
		IntegrationAction: func(_ context.Context, _ string, instanceID string, msg sinks.Message) error {
			gotID = instanceID
			if msg.Type != "message.received" {
				t.Fatal(msg.Type)
			}
			return nil
		},
	}
	ep := &Endpoint{Kind: KindIntegration, SinkConfig: map[string]any{"instanceId": "int_01hxxxxxxxxxxxxxxxxxxxxxxx"}}
	out, err := svc.sendSink(context.Background(), &Delivery{EventID: "evt_1", EventType: "message.received", ID: "dlv_1"},
		ep, events.Event{Type: "message.received"}, []byte(`{}`), nil)
	if err != nil || !out.Success || gotID == "" {
		t.Fatalf("%+v %v id=%s", out, err, gotID)
	}
	svc.IntegrationAction = func(context.Context, string, string, sinks.Message) error {
		return errors.New("temporary unavailable")
	}
	out, err = svc.sendSink(context.Background(), &Delivery{EventID: "evt_1", EventType: "message.received", ID: "dlv_1"},
		ep, events.Event{Type: "message.received"}, []byte(`{}`), nil)
	if err != nil || out.Success {
		t.Fatalf("%+v %v", out, err)
	}
}

func TestMaybeAIEnrich(t *testing.T) {
	svc := &Service{
		AIEnricher: func(_ context.Context, _, _ string, data map[string]any) map[string]any {
			return map[string]any{"summary": "s", "risk": "low"}
		},
	}
	ev := events.Event{Type: events.MessageReceived, EBOrg: "org", EBAccount: "acc", Data: []byte(`{"subject":"Hi"}`)}
	ep := &Endpoint{Payload: PayloadOptions{AIEnrichment: true, Text: "none"}}
	mapped, set := svc.maybeAIEnrich(context.Background(), ev, ep, nil, false)
	if !set {
		t.Fatal("want mapped")
	}
	m, ok := mapped.(map[string]any)
	if !ok {
		t.Fatalf("%T", mapped)
	}
	data, _ := m["data"].(map[string]any)
	ai, _ := data["ai"].(map[string]any)
	if ai["summary"] != "s" {
		t.Fatalf("%v", ai)
	}
}
