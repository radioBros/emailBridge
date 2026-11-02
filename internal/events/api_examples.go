package events

import (
	"encoding/json"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// OpenAPI examples of the /v1/events operations (W70), built from the event examples in examples.go.

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

// exampleEventEnvelope is the stored envelope of a catalogue type's example.
func exampleEventEnvelope(name string) any {
	ct, _ := Lookup(name)
	env, _, _ := exampleFor(ct)
	return env
}

func exampleEventList() map[string]any {
	return map[string]any{
		"data":       []any{exampleEventEnvelope(MessageReceived), exampleEventEnvelope(MessageSent)},
		"nextCursor": "eyJpZCI6ImV2dF8wMW01ZDhjNjUwNWMzZm5rd2Jra25oa3E2MSIsImZpZWxkIjoidGltZSIsImRlc2MiOnRydWV9",
	}
}

// exampleSSEFrames is a stretch of the event stream: two events and a keepalive.
func exampleSSEFrames() string {
	var b strings.Builder
	for _, name := range []string{MessageSent, TrackingOpened} {
		env := exampleEventEnvelope(name)
		raw, _ := json.Marshal(env)
		b.WriteString("id: " + eventExamples[name].ID + "\nevent: " + name + "\ndata: " + string(raw) + "\n\n")
	}
	b.WriteString(":keepalive\n\n")
	return b.String()
}
