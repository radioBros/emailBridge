package gateways

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/submit/policy"
)

// API gateway kinds (P8.3). SMTP remains the implicit default for smtpGateways documents.
const (
	KindSES      = "ses"
	KindPostmark = "postmark"
	KindMailgun  = "mailgun"
	KindSendGrid = "sendgrid"
	KindBrevo    = "brevo"
)

// ProviderConfig is decrypted provider credentials + non-secret options for one send/verify.
type ProviderConfig struct {
	Kind      string
	BaseURL   string            // optional override (tests / private endpoints)
	Region    string            // SES
	Domain    string            // Mailgun
	From      string            // default From when message omits it
	AccessKey string            // SES
	SecretKey string            // SES / generic API secret
	APIKey    string            // Postmark token, SendGrid, Brevo, Mailgun
	Extra     map[string]string // provider-specific
}

// OutboundMail is the MIME + envelope passed to an API provider.
type OutboundMail struct {
	From            string
	To              []string
	Cc              []string
	Bcc             []string
	Subject         string
	HTML            string
	Text            string
	RawMIME         []byte // when set, providers that accept raw use it
	MessageIDHeader string
	OutboundID      string
	Headers         map[string]string
}

// RejectedRecipient is one permanent reject at submit time.
type RejectedRecipient struct {
	Address string
	Code    string
	Detail  string
}

// SendResult is a successful (or partially accepted) API submit.
type SendResult struct {
	ProviderMessageID string
	Accepted          []string
	Rejected          []RejectedRecipient
}

// DeliveryEventType is mapped onto outbound / catalogue states.
type DeliveryEventType string

const (
	DeliverySent       DeliveryEventType = "sent"
	DeliveryBounced    DeliveryEventType = "bounced"
	DeliveryComplained DeliveryEventType = "complained"
	DeliveryFailed     DeliveryEventType = "failed"
	DeliveryDeferred   DeliveryEventType = "deferred"
)

// DeliveryEvent is one provider webhook notification.
type DeliveryEvent struct {
	ProviderMessageID string
	OutboundID        string
	Recipient         string
	Type              DeliveryEventType
	BounceType        string // hard | soft
	Diagnostic        string
	RawType           string
}

// Provider sends mail through an HTTP API and maps delivery webhooks.
type Provider interface {
	Kind() string
	Send(ctx context.Context, client *http.Client, cfg ProviderConfig, msg OutboundMail) (SendResult, error)
	ParseWebhook(headers http.Header, body []byte) ([]DeliveryEvent, error)
}

var providers = map[string]Provider{
	KindSES:      sesProvider{},
	KindPostmark: postmarkProvider{},
	KindMailgun:  mailgunProvider{},
	KindSendGrid: sendgridProvider{},
	KindBrevo:    brevoProvider{},
}

// ProviderFor returns the API provider for a kind.
func ProviderFor(kind string) (Provider, bool) {
	p, ok := providers[strings.ToLower(strings.TrimSpace(kind))]
	return p, ok
}

// APIKinds lists registered API gateway kinds.
func APIKinds() []string {
	out := make([]string, 0, len(providers))
	for k := range providers {
		out = append(out, k)
	}
	return out
}

// ClassifyAPIError maps an HTTP status + body snippet onto an S6 kind and problem code.
func ClassifyAPIError(status int, body string) (errs.Kind, string) {
	low := strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return errs.KindAuth, "smtp_auth_failed"
	case status == http.StatusTooManyRequests || strings.Contains(low, "rate limit") || strings.Contains(low, "throttl"):
		return errs.KindRateLimited, "smtp_rate_limited"
	case status >= 500 || status == 0:
		return errs.KindTemporary, "smtp_temporary"
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return errs.KindTemporary, "smtp_temporary"
	case status >= 400:
		if policy.Throttled(policy.Reply{Code: status, Text: body}) {
			return errs.KindRateLimited, "smtp_rate_limited"
		}
		return errs.KindPermanent, "smtp_permanent"
	default:
		return policy.KindSuccess, ""
	}
}

func requireAPIKey(cfg ProviderConfig) error {
	if strings.TrimSpace(cfg.APIKey) == "" && strings.TrimSpace(cfg.SecretKey) == "" {
		return fmt.Errorf("gateways: api key required")
	}
	return nil
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
