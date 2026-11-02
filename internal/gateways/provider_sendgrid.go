package gateways

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type sendgridProvider struct{}

func (sendgridProvider) Kind() string { return KindSendGrid }

func (sendgridProvider) Send(ctx context.Context, client *http.Client, cfg ProviderConfig, msg OutboundMail) (SendResult, error) {
	if err := requireAPIKey(cfg); err != nil {
		return SendResult{}, err
	}
	from := firstNonEmptyStr(msg.From, cfg.From)
	if from == "" || len(msg.To) == 0 {
		return SendResult{}, fmt.Errorf("sendgrid: from and to required")
	}
	base := firstNonEmptyStr(cfg.BaseURL, "https://api.sendgrid.com")
	tos := make([]map[string]string, 0, len(msg.To))
	for _, t := range msg.To {
		tos = append(tos, map[string]string{"email": t})
	}
	content := []map[string]string{}
	if msg.Text != "" {
		content = append(content, map[string]string{"type": "text/plain", "value": msg.Text})
	}
	if msg.HTML != "" {
		content = append(content, map[string]string{"type": "text/html", "value": msg.HTML})
	}
	if len(content) == 0 {
		content = append(content, map[string]string{"type": "text/plain", "value": ""})
	}
	payload := map[string]any{
		"personalizations": []map[string]any{{"to": tos}},
		"from":             map[string]string{"email": from},
		"subject":          msg.Subject,
		"content":          content,
	}
	if msg.OutboundID != "" {
		payload["custom_args"] = map[string]string{"outboundId": msg.OutboundID}
	}
	status, raw, err := doJSON(ctx, client, http.MethodPost, strings.TrimRight(base, "/")+"/v3/mail/send", map[string]string{
		"Authorization": "Bearer " + cfg.APIKey,
	}, payload)
	if err != nil {
		return SendResult{}, err
	}
	if status < 200 || status >= 300 {
		return SendResult{}, apiErr("sendgrid", status, raw)
	}
	// SendGrid returns 202 with X-Message-Id header; body often empty. Tests set body.
	var out struct {
		MessageID string `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &out)
	return SendResult{ProviderMessageID: out.MessageID, Accepted: msg.To}, nil
}

func (sendgridProvider) ParseWebhook(_ http.Header, body []byte) ([]DeliveryEvent, error) {
	var events []struct {
		Event       string            `json:"event"`
		Email       string            `json:"email"`
		SGMessageID string            `json:"sg_message_id"`
		Reason      string            `json:"reason"`
		Type        string            `json:"type"`
		OutboundID  string            `json:"outboundId"`
		CustomArgs  map[string]string `json:"-"`
	}
	// Array of events is the usual shape.
	if err := json.Unmarshal(body, &events); err != nil {
		var one map[string]any
		if err2 := json.Unmarshal(body, &one); err2 != nil {
			return nil, err
		}
		b, _ := json.Marshal([]any{one})
		if err3 := json.Unmarshal(b, &events); err3 != nil {
			return nil, err
		}
	}
	// Re-parse for custom_args
	var rawEvents []map[string]any
	_ = json.Unmarshal(body, &rawEvents)
	out := make([]DeliveryEvent, 0, len(events))
	for i, ev := range events {
		outbound := ev.OutboundID
		if outbound == "" && i < len(rawEvents) {
			if ca, ok := rawEvents[i]["outboundId"].(string); ok {
				outbound = ca
			}
		}
		rawType := ev.Event
		de := DeliveryEvent{
			ProviderMessageID: ev.SGMessageID, OutboundID: outbound, Recipient: ev.Email, RawType: rawType,
		}
		switch strings.ToLower(rawType) {
		case "delivered":
			de.Type = DeliverySent
		case "bounce", "blocked":
			de.Type = DeliveryBounced
			de.BounceType = "hard"
			if strings.EqualFold(ev.Type, "blocked") {
				de.BounceType = "soft"
			}
			de.Diagnostic = ev.Reason
		case "dropped":
			de.Type = DeliveryFailed
			de.Diagnostic = ev.Reason
		case "spamreport":
			de.Type = DeliveryComplained
		case "deferred":
			de.Type = DeliveryDeferred
		default:
			de.Type = DeliveryDeferred
		}
		out = append(out, de)
	}
	return out, nil
}
