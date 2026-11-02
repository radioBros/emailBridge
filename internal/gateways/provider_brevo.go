package gateways

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type brevoProvider struct{}

func (brevoProvider) Kind() string { return KindBrevo }

func (brevoProvider) Send(ctx context.Context, client *http.Client, cfg ProviderConfig, msg OutboundMail) (SendResult, error) {
	if err := requireAPIKey(cfg); err != nil {
		return SendResult{}, err
	}
	from := firstNonEmptyStr(msg.From, cfg.From)
	if from == "" || len(msg.To) == 0 {
		return SendResult{}, fmt.Errorf("brevo: from and to required")
	}
	base := firstNonEmptyStr(cfg.BaseURL, "https://api.brevo.com")
	tos := make([]map[string]string, 0, len(msg.To))
	for _, t := range msg.To {
		tos = append(tos, map[string]string{"email": t})
	}
	payload := map[string]any{
		"sender":      map[string]string{"email": from},
		"to":          tos,
		"subject":     msg.Subject,
		"htmlContent": msg.HTML,
		"textContent": msg.Text,
	}
	if msg.OutboundID != "" {
		payload["headers"] = map[string]string{"X-Mailin-custom": msg.OutboundID}
		payload["tags"] = []string{msg.OutboundID}
	}
	status, raw, err := doJSON(ctx, client, http.MethodPost, strings.TrimRight(base, "/")+"/v3/smtp/email", map[string]string{
		"api-key": cfg.APIKey,
		"Accept":  "application/json",
	}, payload)
	if err != nil {
		return SendResult{}, err
	}
	if status < 200 || status >= 300 {
		return SendResult{}, apiErr("brevo", status, raw)
	}
	var out struct {
		MessageID string `json:"messageId"`
	}
	_ = json.Unmarshal(raw, &out)
	return SendResult{ProviderMessageID: out.MessageID, Accepted: msg.To}, nil
}

func (brevoProvider) ParseWebhook(_ http.Header, body []byte) ([]DeliveryEvent, error) {
	var ev struct {
		Event     string `json:"event"`
		Email     string `json:"email"`
		MessageID string `json:"message-id"`
		Tag       string `json:"tag"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, err
	}
	rawType := ev.Event
	de := DeliveryEvent{
		ProviderMessageID: ev.MessageID, OutboundID: ev.Tag, Recipient: ev.Email, RawType: rawType,
	}
	switch strings.ToLower(rawType) {
	case "delivered":
		de.Type = DeliverySent
	case "hard_bounce", "soft_bounce":
		de.Type = DeliveryBounced
		de.BounceType = "hard"
		if strings.HasPrefix(strings.ToLower(rawType), "soft") {
			de.BounceType = "soft"
		}
		de.Diagnostic = ev.Reason
	case "spam", "complaint":
		de.Type = DeliveryComplained
	case "blocked", "invalid_email", "error":
		de.Type = DeliveryFailed
		de.Diagnostic = ev.Reason
	case "deferred":
		de.Type = DeliveryDeferred
	default:
		de.Type = DeliveryDeferred
	}
	return []DeliveryEvent{de}, nil
}
