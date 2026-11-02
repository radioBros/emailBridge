package connectors

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/radiobros/emailbridge/internal/gateways"
)

// Sending connector IDs (P8.3 — co-owned folder; defs materialised by parent).
const (
	TypeSES      = gateways.KindSES
	TypePostmark = gateways.KindPostmark
	TypeMailgun  = gateways.KindMailgun
	TypeSendGrid = gateways.KindSendGrid
	TypeBrevo    = gateways.KindBrevo
)

// SES returns the Amazon SES API gateway connector.
func SES() Def {
	return Def{
		ID: TypeSES, Version: "1.0.0", Name: "Amazon SES", Category: CategorySending,
		Icon: "mdi-aws", Color: "#FF9900",
		Description: "Send mail through Amazon SES (API v2, SigV4). Install creates a sending gateway (manage under Sending → Gateways). Message content leaves EmailBridge to AWS.",
		DocsPath:    "/docs/integrations/ses",
		ConfigSchema: sendingSchema(map[string]any{
			"region":    map[string]any{"type": "string", "title": "AWS region", "default": "eu-west-1"},
			"accessKey": map[string]any{"type": "string", "title": "Access key ID"},
			"secretKey": map[string]any{"type": "string", "writeOnly": true, "title": "Secret access key"},
			"baseUrl":   map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
		}, "region", "accessKey", "secretKey", "from"),
		SecretFields: []string{"secretKey"},
		Capabilities: []string{CapTest, CapInboundCallback},
		Settings: map[string]string{
			"integrations.ses.region": "{{config.region}}",
			"integrations.ses.from":   "{{config.from}}",
		},
		TestFunc: HTTPTester("SES", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			region := strCfg(cfg, "region")
			base := strCfg(cfg, "baseUrl")
			if base == "" {
				base = fmt.Sprintf("https://email.%s.amazonaws.com", region)
			}
			return http.MethodGet, strings.TrimRight(base, "/") + "/", map[string]string{
				"X-Amz-Target": "probe",
			}, nil, nil
		}),
	}
}

// Postmark returns the Postmark API gateway connector.
func Postmark() Def {
	return Def{
		ID: TypePostmark, Version: "1.0.0", Name: "Postmark", Category: CategorySending,
		Icon: "mdi-email-fast", Color: "#FFDE00",
		Description: "Send mail through Postmark. Full message content leaves EmailBridge.",
		DocsPath:    "/docs/integrations/postmark",
		ConfigSchema: sendingSchema(map[string]any{
			"serverToken": map[string]any{"type": "string", "writeOnly": true, "title": "Server API token"},
			"baseUrl":     map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
		}, "serverToken", "from"),
		SecretFields: []string{"serverToken"},
		Capabilities: []string{CapTest, CapInboundCallback},
		Settings:     map[string]string{"integrations.postmark.from": "{{config.from}}"},
		TestFunc: HTTPTester("Postmark", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			tok := secrets["serverToken"]
			if tok == "" {
				return "", "", nil, nil, fmt.Errorf("serverToken required")
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://api.postmarkapp.com")
			return http.MethodGet, strings.TrimRight(base, "/") + "/server", map[string]string{
				"X-Postmark-Server-Token": tok, "Accept": "application/json",
			}, nil, nil
		}),
	}
}

// Mailgun returns the Mailgun API gateway connector.
func Mailgun() Def {
	return Def{
		ID: TypeMailgun, Version: "1.0.0", Name: "Mailgun", Category: CategorySending,
		Icon: "mdi-email-outline", Color: "#F06B66",
		Description: "Send mail through Mailgun. Message content leaves EmailBridge.",
		DocsPath:    "/docs/integrations/mailgun",
		ConfigSchema: sendingSchema(map[string]any{
			"domain":  map[string]any{"type": "string", "title": "Sending domain"},
			"apiKey":  map[string]any{"type": "string", "writeOnly": true, "title": "API key"},
			"baseUrl": map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
		}, "domain", "apiKey", "from"),
		SecretFields: []string{"apiKey"},
		Capabilities: []string{CapTest, CapInboundCallback},
		Settings: map[string]string{
			"integrations.mailgun.domain": "{{config.domain}}",
			"integrations.mailgun.from":   "{{config.from}}",
		},
		TestFunc: HTTPTester("Mailgun", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			key, domain := secrets["apiKey"], strCfg(cfg, "domain")
			if key == "" || domain == "" {
				return "", "", nil, nil, fmt.Errorf("apiKey and domain required")
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://api.mailgun.net")
			return http.MethodGet, strings.TrimRight(base, "/") + "/v3/domains/" + domain, map[string]string{
				"Authorization": "Basic " + basicAuthMailgun(key),
			}, nil, nil
		}),
	}
}

// SendGrid returns the SendGrid API gateway connector.
func SendGrid() Def {
	return Def{
		ID: TypeSendGrid, Version: "1.0.0", Name: "SendGrid", Category: CategorySending,
		Icon: "mdi-send", Color: "#1A82E2",
		Description: "Send mail through Twilio SendGrid. Message content leaves EmailBridge.",
		DocsPath:    "/docs/integrations/sendgrid",
		ConfigSchema: sendingSchema(map[string]any{
			"apiKey":  map[string]any{"type": "string", "writeOnly": true, "title": "API key"},
			"baseUrl": map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
		}, "apiKey", "from"),
		SecretFields: []string{"apiKey"},
		Capabilities: []string{CapTest, CapInboundCallback},
		Settings:     map[string]string{"integrations.sendgrid.from": "{{config.from}}"},
		TestFunc: HTTPTester("SendGrid", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			key := secrets["apiKey"]
			if key == "" {
				return "", "", nil, nil, fmt.Errorf("apiKey required")
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://api.sendgrid.com")
			return http.MethodGet, strings.TrimRight(base, "/") + "/v3/user/profile", map[string]string{
				"Authorization": "Bearer " + key,
			}, nil, nil
		}),
	}
}

// Brevo returns the Brevo API gateway connector.
func Brevo() Def {
	return Def{
		ID: TypeBrevo, Version: "1.0.0", Name: "Brevo", Category: CategorySending,
		Icon: "mdi-email-check", Color: "#0B996E",
		Description: "Send mail through Brevo. Message content leaves EmailBridge.",
		DocsPath:    "/docs/integrations/brevo",
		ConfigSchema: sendingSchema(map[string]any{
			"apiKey":  map[string]any{"type": "string", "writeOnly": true, "title": "API key"},
			"baseUrl": map[string]any{"type": "string", "format": "uri", "title": "API base URL override"},
		}, "apiKey", "from"),
		SecretFields: []string{"apiKey"},
		Capabilities: []string{CapTest, CapInboundCallback},
		Settings:     map[string]string{"integrations.brevo.from": "{{config.from}}"},
		TestFunc: HTTPTester("Brevo", func(cfg map[string]any, secrets map[string]string) (string, string, map[string]string, []byte, error) {
			key := secrets["apiKey"]
			if key == "" {
				return "", "", nil, nil, fmt.Errorf("apiKey required")
			}
			base := firstNonEmpty(strCfg(cfg, "baseUrl"), "https://api.brevo.com")
			return http.MethodGet, strings.TrimRight(base, "/") + "/v3/account", map[string]string{
				"api-key": key, "Accept": "application/json",
			}, nil, nil
		}),
	}
}

func basicAuthMailgun(apiKey string) string {
	return base64.StdEncoding.EncodeToString([]byte("api:" + apiKey))
}
