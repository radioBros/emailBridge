package events

import (
	"encoding/json"
	"testing"
	"time"
)

func TestEnvelopeJSONTags(t *testing.T) {
	e := Envelope{
		SpecVersion:     "1.0",
		ID:              "evt_01jbf3m2c8x7v4q9w5e6r1t0yz",
		Type:            "message.received",
		Source:          "/organizations/org_1/accounts/acc_1",
		Subject:         "msg_1",
		Time:            time.Date(2026, 10, 25, 14, 3, 11, 482000000, time.UTC),
		DataContentType: "application/json",
		DataSchema:      "https://emailbridge.dev/schemas/events/message.received/v1.json",
		EBOrg:           "org_1",
		EBAccount:       "acc_1",
		EBSchema:        1,
		TraceParent:     "00-abc-def-01",
		Data:            json.RawMessage(`{"messageId":"msg_1"}`),
	}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{
		"specversion", "id", "type", "source", "subject", "time",
		"datacontenttype", "dataschema", "eborg", "ebaccount", "ebschema", "traceparent", "data",
	} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing JSON key %q in %s", k, b)
		}
	}
}
