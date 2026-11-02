package gateways

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type mailgunProvider struct{}

func (mailgunProvider) Kind() string { return KindMailgun }

func (mailgunProvider) Send(ctx context.Context, client *http.Client, cfg ProviderConfig, msg OutboundMail) (SendResult, error) {
	if err := requireAPIKey(cfg); err != nil {
		return SendResult{}, err
	}
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" {
		return SendResult{}, fmt.Errorf("mailgun: domain required")
	}
	from := firstNonEmptyStr(msg.From, cfg.From)
	if from == "" || len(msg.To) == 0 {
		return SendResult{}, fmt.Errorf("mailgun: from and to required")
	}
	base := firstNonEmptyStr(cfg.BaseURL, "https://api.mailgun.net")
	form := url.Values{}
	form.Set("from", from)
	for _, t := range msg.To {
		form.Add("to", t)
	}
	form.Set("subject", msg.Subject)
	if msg.HTML != "" {
		form.Set("html", msg.HTML)
	}
	if msg.Text != "" {
		form.Set("text", msg.Text)
	}
	if msg.OutboundID != "" {
		form.Set("v:outboundId", msg.OutboundID)
	}
	endpoint := strings.TrimRight(base, "/") + "/v3/" + domain + "/messages"
	status, raw, err := doForm(ctx, client, endpoint, map[string]string{
		"Authorization": "Basic " + basicAuth("api", cfg.APIKey),
	}, form)
	if err != nil {
		return SendResult{}, err
	}
	if status < 200 || status >= 300 {
		return SendResult{}, apiErr("mailgun", status, raw)
	}
	var out struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &out)
	return SendResult{ProviderMessageID: strings.Trim(out.ID, "<>"), Accepted: msg.To}, nil
}

func (mailgunProvider) ParseWebhook(_ http.Header, body []byte) ([]DeliveryEvent, error) {
	var wrap struct {
		EventData json.RawMessage `json:"event-data"`
	}
	payload := body
	if err := json.Unmarshal(body, &wrap); err == nil && len(wrap.EventData) > 0 {
		payload = wrap.EventData
	}
	var ev struct {
		Event   string `json:"event"`
		ID      string `json:"id"`
		Message struct {
			Headers map[string]string `json:"headers"`
		} `json:"message"`
		Recipient string         `json:"recipient"`
		UserVars  map[string]any `json:"user-variables"`
		Severity  string         `json:"severity"`
		Reason    string         `json:"reason"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, err
	}
	outbound := ""
	if ev.UserVars != nil {
		if v, ok := ev.UserVars["outboundId"].(string); ok {
			outbound = v
		}
	}
	mid := ev.ID
	if mid == "" && ev.Message.Headers != nil {
		mid = ev.Message.Headers["message-id"]
	}
	rawType := ev.Event
	switch strings.ToLower(rawType) {
	case "delivered":
		return []DeliveryEvent{{ProviderMessageID: mid, OutboundID: outbound, Recipient: ev.Recipient, Type: DeliverySent, RawType: rawType}}, nil
	case "failed", "rejected":
		bt := "hard"
		if strings.EqualFold(ev.Severity, "temporary") {
			bt = "soft"
		}
		typ := DeliveryFailed
		if rawType == "failed" {
			typ = DeliveryBounced
		}
		return []DeliveryEvent{{
			ProviderMessageID: mid, OutboundID: outbound, Recipient: ev.Recipient,
			Type: typ, BounceType: bt, Diagnostic: ev.Reason, RawType: rawType,
		}}, nil
	case "complained":
		return []DeliveryEvent{{ProviderMessageID: mid, OutboundID: outbound, Recipient: ev.Recipient, Type: DeliveryComplained, RawType: rawType}}, nil
	default:
		return []DeliveryEvent{{ProviderMessageID: mid, OutboundID: outbound, Recipient: ev.Recipient, Type: DeliveryDeferred, RawType: rawType}}, nil
	}
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
