package gateways

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

type sesProvider struct{}

func (sesProvider) Kind() string { return KindSES }

func (sesProvider) Send(ctx context.Context, client *http.Client, cfg ProviderConfig, msg OutboundMail) (SendResult, error) {
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return SendResult{}, fmt.Errorf("ses: access key and secret required")
	}
	region := firstNonEmptyStr(cfg.Region, "us-east-1")
	base := firstNonEmptyStr(cfg.BaseURL, fmt.Sprintf("https://email.%s.amazonaws.com", region))
	base = strings.TrimRight(base, "/")
	from := firstNonEmptyStr(msg.From, cfg.From)
	if from == "" {
		return SendResult{}, fmt.Errorf("ses: from required")
	}
	to := append([]string{}, msg.To...)
	if len(to) == 0 {
		return SendResult{}, fmt.Errorf("ses: to required")
	}
	payload := map[string]any{
		"FromEmailAddress": from,
		"Destination":      map[string]any{"ToAddresses": to},
		"Content": map[string]any{
			"Simple": map[string]any{
				"Subject": map[string]any{"Data": msg.Subject},
				"Body":    sesBody(msg),
			},
		},
	}
	var tags []map[string]string
	if msg.MessageIDHeader != "" {
		tags = append(tags, map[string]string{"Name": "eb_message_id", "Value": msg.MessageIDHeader})
	}
	if msg.OutboundID != "" {
		tags = append(tags, map[string]string{"Name": "eb_outbound_id", "Value": msg.OutboundID})
	}
	if len(tags) > 0 {
		payload["EmailTags"] = tags
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return SendResult{}, err
	}
	url := base + "/v2/email/outbound-emails"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	credProvider := credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, "")
	creds, err := credProvider.Retrieve(ctx)
	if err != nil {
		return SendResult{}, err
	}
	signer := v4.NewSigner()
	if err := signer.SignHTTP(ctx, creds, req, payloadHash, "ses", region, time.Now().UTC()); err != nil {
		return SendResult{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return SendResult{}, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		kind, code := ClassifyAPIError(res.StatusCode, string(raw))
		return SendResult{}, fmt.Errorf("ses: HTTP %d kind=%s code=%s: %s", res.StatusCode, kind, code, truncate(string(raw), 200))
	}
	var out struct {
		MessageID string `json:"MessageId"`
	}
	_ = json.Unmarshal(raw, &out)
	return SendResult{ProviderMessageID: out.MessageID, Accepted: to}, nil
}

func (sesProvider) ParseWebhook(_ http.Header, body []byte) ([]DeliveryEvent, error) {
	var env struct {
		Type    string `json:"Type"`
		Message string `json:"Message"`
	}
	payload := body
	if err := json.Unmarshal(body, &env); err == nil && env.Type == "Notification" && env.Message != "" {
		payload = []byte(env.Message)
	}
	var ev struct {
		EventType        string `json:"eventType"`
		NotificationType string `json:"notificationType"`
		Mail             struct {
			MessageID string              `json:"messageId"`
			Tags      map[string][]string `json:"tags"`
		} `json:"mail"`
		Bounce struct {
			BounceType        string `json:"bounceType"`
			BouncedRecipients []struct {
				EmailAddress   string `json:"emailAddress"`
				DiagnosticCode string `json:"diagnosticCode"`
			} `json:"bouncedRecipients"`
		} `json:"bounce"`
		Complaint struct {
			ComplainedRecipients []struct {
				EmailAddress string `json:"emailAddress"`
			} `json:"complainedRecipients"`
		} `json:"complaint"`
		Delivery struct {
			Recipients []string `json:"recipients"`
		} `json:"delivery"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		return nil, err
	}
	rawType := firstNonEmptyStr(ev.EventType, ev.NotificationType)
	outbound := firstTag(ev.Mail.Tags, "eb_outbound_id")
	mid := ev.Mail.MessageID
	switch strings.ToLower(rawType) {
	case "delivery":
		out := make([]DeliveryEvent, 0, max(1, len(ev.Delivery.Recipients)))
		for _, r := range ev.Delivery.Recipients {
			out = append(out, DeliveryEvent{
				ProviderMessageID: mid, OutboundID: outbound, Recipient: r,
				Type: DeliverySent, RawType: rawType,
			})
		}
		if len(out) == 0 {
			out = append(out, DeliveryEvent{ProviderMessageID: mid, OutboundID: outbound, Type: DeliverySent, RawType: rawType})
		}
		return out, nil
	case "bounce":
		bt := "hard"
		if strings.EqualFold(ev.Bounce.BounceType, "Transient") {
			bt = "soft"
		}
		out := make([]DeliveryEvent, 0, len(ev.Bounce.BouncedRecipients))
		for _, r := range ev.Bounce.BouncedRecipients {
			out = append(out, DeliveryEvent{
				ProviderMessageID: mid, OutboundID: outbound, Recipient: r.EmailAddress,
				Type: DeliveryBounced, BounceType: bt, Diagnostic: r.DiagnosticCode, RawType: rawType,
			})
		}
		return out, nil
	case "complaint":
		out := make([]DeliveryEvent, 0, len(ev.Complaint.ComplainedRecipients))
		for _, r := range ev.Complaint.ComplainedRecipients {
			out = append(out, DeliveryEvent{
				ProviderMessageID: mid, OutboundID: outbound, Recipient: r.EmailAddress,
				Type: DeliveryComplained, RawType: rawType,
			})
		}
		return out, nil
	case "reject", "rendering failure", "rejectevent":
		return []DeliveryEvent{{
			ProviderMessageID: mid, OutboundID: outbound, Type: DeliveryFailed, RawType: rawType,
		}}, nil
	default:
		return []DeliveryEvent{{
			ProviderMessageID: mid, OutboundID: outbound, Type: DeliveryDeferred, RawType: rawType,
		}}, nil
	}
}

func sesBody(msg OutboundMail) map[string]any {
	body := map[string]any{}
	if msg.HTML != "" {
		body["Html"] = map[string]any{"Data": msg.HTML}
	}
	if msg.Text != "" {
		body["Text"] = map[string]any{"Data": msg.Text}
	}
	if len(body) == 0 {
		body["Text"] = map[string]any{"Data": ""}
	}
	return body
}

func firstTag(tags map[string][]string, key string) string {
	if tags == nil {
		return ""
	}
	if v := tags[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
