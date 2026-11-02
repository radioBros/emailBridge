package webhook

import (
	"encoding/json"
	"testing"

	"github.com/radiobros/emailbridge/internal/events"
)

func TestTrimData_attachmentMaxAndReplyText(t *testing.T) {
	ev := events.Event{
		Type: events.MessageReceived,
		Data: json.RawMessage(`{
			"attachments":[{"partId":"1"},{"partId":"2"},{"partId":"3"}],
			"replyText":"hello",
			"text":{"plain":"x"}
		}`),
	}
	body, err := buildBody(ev, nil, false, PayloadOptions{Attachments: true, AttachmentMax: 2, ReplyText: false, Text: "none"})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	data := envelope["data"].(map[string]any)
	atts := data["attachments"].([]any)
	if len(atts) != 2 {
		t.Fatalf("attachmentMax: %d", len(atts))
	}
	if _, ok := data["replyText"]; ok {
		t.Fatal("replyText should be stripped")
	}
}
