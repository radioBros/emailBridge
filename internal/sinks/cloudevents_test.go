package sinks

import "testing"

func TestCloudEventBinaryHeaders(t *testing.T) {
	t.Parallel()
	h := cloudEventBinaryHeaders(Message{
		ID: "evt_1", Type: "message.received", Subject: "inbox", AccountID: "acc",
		Headers: map[string]string{"source": "https://eb.example", "content-type": "application/cloudevents+json", "x-custom": "1"},
		Body:    []byte(`{}`),
	})
	if h["ce_id"] != "evt_1" || h["ce_type"] != "message.received" || h["ce_subject"] != "inbox" {
		t.Fatalf("core headers: %+v", h)
	}
	if h["ce_specversion"] != "1.0" {
		t.Fatal("specversion")
	}
	if h["ce_source"] != "https://eb.example" {
		t.Fatalf("source: %q", h["ce_source"])
	}
	if h["content-type"] != "application/cloudevents+json" {
		t.Fatalf("content-type: %q", h["content-type"])
	}
	if h["x-custom"] != "1" {
		t.Fatal("extension missing")
	}
}

func TestMessageAttributes(t *testing.T) {
	t.Parallel()
	a := messageAttributes(Message{ID: "evt_1", Type: "t", AccountID: "acc", Subject: "s", Headers: map[string]string{"foo": "bar"}})
	if a["eventId"] != "evt_1" || a["eventType"] != "t" || a["accountId"] != "acc" || a["subject"] != "s" || a["foo"] != "bar" {
		t.Fatalf("%+v", a)
	}
}
