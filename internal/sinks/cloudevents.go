package sinks

import "strings"

// cloudEventBinaryHeaders builds Kafka CloudEvents binary-mode headers (ce_*).
func cloudEventBinaryHeaders(m Message) map[string]string {
	out := map[string]string{
		"ce_specversion": "1.0",
		"ce_id":          m.ID,
		"ce_type":        m.Type,
	}
	if m.Subject != "" {
		out["ce_subject"] = m.Subject
	}
	contentType := "application/json"
	source := ""
	for k, v := range m.Headers {
		lk := strings.ToLower(k)
		switch {
		case lk == "content-type" || lk == "datacontenttype":
			contentType = v
		case lk == "ce_source" || lk == "source":
			source = v
		case strings.HasPrefix(lk, "ce_"):
			out[lk] = v
		default:
			// Pass through non-reserved headers as extensions (lowercase).
			if _, reserved := out[lk]; !reserved && lk != "content-type" {
				out[lk] = v
			}
		}
	}
	out["content-type"] = contentType
	if source != "" {
		out["ce_source"] = source
	} else if _, ok := out["ce_source"]; !ok {
		out["ce_source"] = "emailbridge"
	}
	return out
}

// messageAttributes builds SQS/SNS/Pub/Sub string attributes from the message.
func messageAttributes(m Message) map[string]string {
	out := map[string]string{
		"eventId":   m.ID,
		"eventType": m.Type,
	}
	if m.AccountID != "" {
		out["accountId"] = m.AccountID
	}
	if m.Subject != "" {
		out["subject"] = m.Subject
	}
	for k, v := range m.Headers {
		lk := strings.ToLower(k)
		if lk == "eventid" || lk == "eventtype" || lk == "accountid" || lk == "subject" {
			continue
		}
		out[k] = v
	}
	return out
}
