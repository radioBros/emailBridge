package gateways

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type postmarkProvider struct{}

func (postmarkProvider) Kind() string { return KindPostmark }

func (postmarkProvider) Send(ctx context.Context, client *http.Client, cfg ProviderConfig, msg OutboundMail) (SendResult, error) {
	if err := requireAPIKey(cfg); err != nil {
		return SendResult{}, err
	}
	from := firstNonEmptyStr(msg.From, cfg.From)
	if from == "" || len(msg.To) == 0 {
		return SendResult{}, fmt.Errorf("postmark: from and to required")
	}
	base := firstNonEmptyStr(cfg.BaseURL, "https://api.postmarkapp.com")
	payload := map[string]any{
		"From":          from,
		"To":            strings.Join(msg.To, ","),
		"Subject":       msg.Subject,
		"HtmlBody":      msg.HTML,
		"TextBody":      msg.Text,
		"MessageStream": "outbound",
	}
	if msg.OutboundID != "" {
		payload["Metadata"] = map[string]string{"outboundId": msg.OutboundID}
	}
	status, raw, err := doJSON(ctx, client, http.MethodPost, strings.TrimRight(base, "/")+"/email", map[string]string{
		"X-Postmark-Server-Token": cfg.APIKey,
		"Accept":                  "application/json",
	}, payload)
	if err != nil {
		return SendResult{}, err
	}
	if status < 200 || status >= 300 {
		return SendResult{}, apiErr("postmark", status, raw)
	}
	var out struct {
		MessageID string `json:"MessageID"`
		To        string `json:"To"`
	}
	_ = json.Unmarshal(raw, &out)
	return SendResult{ProviderMessageID: out.MessageID, Accepted: msg.To}, nil
}

func (postmarkProvider) ParseWebhook(_ http.Header, body []byte) ([]DeliveryEvent, error) {
	var ev struct {
		RecordType  string            `json:"RecordType"`
		Type        string            `json:"Type"`
		MessageID   string            `json:"MessageID"`
		Email       string            `json:"Email"`
		Recipient   string            `json:"Recipient"`
		Description string            `json:"Description"`
		Metadata    map[string]string `json:"Metadata"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return nil, err
	}
	rawType := firstNonEmptyStr(ev.RecordType, ev.Type)
	recipient := firstNonEmptyStr(ev.Email, ev.Recipient)
	outbound := ""
	if ev.Metadata != nil {
		outbound = ev.Metadata["outboundId"]
	}
	switch strings.ToLower(rawType) {
	case "delivery":
		return []DeliveryEvent{{
			ProviderMessageID: ev.MessageID, OutboundID: outbound, Recipient: recipient,
			Type: DeliverySent, RawType: rawType,
		}}, nil
	case "bounce":
		bt := "hard"
		if strings.Contains(strings.ToLower(ev.Type), "soft") {
			bt = "soft"
		}
		return []DeliveryEvent{{
			ProviderMessageID: ev.MessageID, OutboundID: outbound, Recipient: recipient,
			Type: DeliveryBounced, BounceType: bt, Diagnostic: ev.Description, RawType: rawType,
		}}, nil
	case "spamcomplaint":
		return []DeliveryEvent{{
			ProviderMessageID: ev.MessageID, OutboundID: outbound, Recipient: recipient,
			Type: DeliveryComplained, RawType: rawType,
		}}, nil
	default:
		return []DeliveryEvent{{
			ProviderMessageID: ev.MessageID, OutboundID: outbound, Recipient: recipient,
			Type: DeliveryDeferred, RawType: rawType,
		}}, nil
	}
}
