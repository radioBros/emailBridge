package webhook

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/radiobros/emailbridge/internal/events"
)

var reservedHeaders = map[string]bool{
	"content-type": true, "user-agent": true,
	"webhook-id": true, "webhook-timestamp": true, "webhook-signature": true,
	"eb-signature": true, "eb-event-type": true, "eb-delivery-id": true,
	"eb-attempt": true, "eb-replay": true, "traceparent": true,
}

func endpointMatches(endpoint *Endpoint, event events.Event) bool {
	if endpoint == nil || !endpoint.Enabled || event.Type == events.EndpointTest || !containsEvent(endpoint.Events, event.Type) {
		return false
	}
	if len(endpoint.Filter.AccountIDs) > 0 && !slices.Contains(endpoint.Filter.AccountIDs, event.EBAccount) {
		return false
	}
	if len(endpoint.Filter.MailboxPaths) == 0 {
		return true
	}
	var data map[string]any
	if json.Unmarshal(event.Data, &data) != nil {
		return false
	}
	mailbox, _ := data["mailbox"].(map[string]any)
	path, _ := mailbox["path"].(string)
	return slices.Contains(endpoint.Filter.MailboxPaths, path)
}

func containsEvent(allow []string, typ string) bool {
	return slices.Contains(allow, "*") || slices.Contains(allow, typ)
}

func buildBody(event events.Event, mapped any, mappedSet bool, payload PayloadOptions) ([]byte, error) {
	if mappedSet {
		return json.Marshal(mapped)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	data, _ := envelope["data"].(map[string]any)
	trimData(data, payload)
	return json.Marshal(envelope)
}

func trimData(data map[string]any, payload PayloadOptions) {
	if data == nil {
		return
	}
	switch payload.Text {
	case "plain":
		trimText(data, payload.TextMax, true, false)
	case "html":
		trimText(data, payload.TextMax, false, true)
	case "both":
		trimText(data, payload.TextMax, true, true)
	default:
		delete(data, "text")
	}
	trimHeaders(data, payload.Headers)
	if !payload.Attachments {
		delete(data, "attachments")
	} else if payload.AttachmentMax > 0 {
		if atts, ok := data["attachments"].([]any); ok && len(atts) > payload.AttachmentMax {
			data["attachments"] = atts[:payload.AttachmentMax]
		}
	}
	if !payload.WebSafeHTML {
		delete(data, "webSafeHtml")
	}
	if !payload.CalendarEvents {
		delete(data, "calendarEvents")
	}
	if !payload.ReplyText {
		delete(data, "replyText")
	}
}

func trimText(data map[string]any, maxBytes int, plain, html bool) {
	text, ok := data["text"].(map[string]any)
	if !ok {
		return
	}
	if !plain {
		delete(text, "plain")
	}
	if !html {
		delete(text, "html")
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	truncated := false
	for _, key := range []string{"plain", "html"} {
		value, ok := text[key].(string)
		if !ok || len(value) <= maxBytes {
			continue
		}
		text[key] = validPrefix(value, maxBytes)
		truncated = true
	}
	if truncated {
		text["truncated"] = true
	}
}

func validPrefix(value string, limit int) string {
	if limit >= len(value) {
		return value
	}
	for limit > 0 && (value[limit]&0xc0) == 0x80 {
		limit--
	}
	return value[:limit]
}

func trimHeaders(data map[string]any, wanted []string) {
	if slices.Contains(wanted, "*") {
		return
	}
	if len(wanted) == 0 {
		delete(data, "headers")
		return
	}
	headers, ok := data["headers"].(map[string]any)
	if !ok {
		delete(data, "headers")
		return
	}
	allow := make(map[string]bool, len(wanted))
	for _, name := range wanted {
		allow[strings.ToLower(name)] = true
	}
	for name := range headers {
		if !allow[strings.ToLower(name)] {
			delete(headers, name)
		}
	}
}

func payloadOverride(base PayloadOptions, override map[string]any) PayloadOptions {
	if len(override) == 0 {
		return base
	}
	raw, err := json.Marshal(override)
	if err != nil {
		return base
	}
	var patch PayloadOptions
	if json.Unmarshal(raw, &patch) != nil {
		return base
	}
	if _, ok := override["text"]; ok {
		base.Text = patch.Text
	}
	if _, ok := override["textMax"]; ok {
		base.TextMax = patch.TextMax
	}
	if _, ok := override["headers"]; ok {
		base.Headers = patch.Headers
	}
	if _, ok := override["attachments"]; ok {
		base.Attachments = patch.Attachments
	}
	if _, ok := override["attachmentMax"]; ok {
		base.AttachmentMax = patch.AttachmentMax
	}
	if _, ok := override["webSafeHtml"]; ok {
		base.WebSafeHTML = patch.WebSafeHTML
	}
	if _, ok := override["calendarEvents"]; ok {
		base.CalendarEvents = patch.CalendarEvents
	}
	if _, ok := override["replyText"]; ok {
		base.ReplyText = patch.ReplyText
	}
	return base
}

func addCustomHeaders(dst http.Header, headers map[string]string) error {
	for name, value := range headers {
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		if name == "" {
			return errors.New("webhook: empty custom header name")
		}
		if reservedHeaders[strings.ToLower(name)] {
			continue
		}
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("webhook: custom header contains a newline")
		}
		dst.Set(name, value)
	}
	return nil
}
