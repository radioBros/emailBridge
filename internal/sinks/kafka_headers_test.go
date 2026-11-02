package sinks

import (
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestKafkaRecordMapping(t *testing.T) {
	t.Parallel()
	m := Message{
		ID: "evt_x", Type: "message.received", AccountID: "acc", Subject: "subj",
		Body: []byte(`{"ok":true}`), Headers: map[string]string{"source": "eb"},
	}
	headers := cloudEventBinaryHeaders(m)
	rec := &kgo.Record{Topic: ExpandTemplate("eb.{type}", m), Key: []byte(m.AccountID), Value: m.Body}
	for k, v := range headers {
		rec.Headers = append(rec.Headers, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}
	if rec.Topic != "eb.message.received" {
		t.Fatalf("topic %q", rec.Topic)
	}
	if string(rec.Key) != "acc" {
		t.Fatalf("key %q", rec.Key)
	}
	found := map[string]string{}
	for _, h := range rec.Headers {
		found[h.Key] = string(h.Value)
	}
	if found["ce_id"] != "evt_x" || found["ce_source"] != "eb" || found["content-type"] == "" {
		t.Fatalf("%+v", found)
	}
}
