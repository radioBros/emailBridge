package connectors

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FeatherDesk returns the FeatherDesk helpdesk connector (email ↔ ticket).
func FeatherDesk() Def {
	return Def{
		ID: IDFeatherDesk, Version: "1.0.0", Name: "FeatherDesk", Category: CategoryHelpdesk,
		Icon: "mdi-deskphone", Color: "#0D9488",
		Description: "Create FeatherDesk tickets from inbound mail and send ticket replies " +
			"back through EmailBridge. Message subjects, addresses and body text leave EmailBridge.",
		DocsPath: "/docs/integrations/featherdesk",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"baseUrl", "apiKey", "accountId", "callbackToken"},
			"properties": map[string]any{
				"baseUrl": map[string]any{
					"type": "string", "format": "uri", "title": "FeatherDesk base URL",
					"description": "e.g. https://desk.example.com",
				},
				"apiKey": map[string]any{
					"type": "string", "writeOnly": true, "title": "API key",
				},
				"accountId": map[string]any{
					"type": "string", "title": "Send account ID",
					"description": "EmailBridge account used for ticket-reply → mail.",
					"pattern":     "^acc_",
				},
				"mailboxId": map[string]any{
					"type": "string", "title": "Mailbox ID", "default": "",
				},
				"callbackToken": map[string]any{
					"type": "string", "writeOnly": true, "title": "Inbound callback token",
					"description": "Shared secret for POST /integrations/{id}/callback (HMAC).",
				},
				"events": map[string]any{
					"type": "string", "title": "Email→ticket filter CEL",
					"default": `type == "message.received"`,
				},
			},
		},
		SecretFields: []string{"apiKey", "callbackToken"},
		Capabilities: []string{CapTest, CapInboundCallback},
		Endpoints: []EndpointTmpl{{
			Name: "FeatherDesk tickets",
			URL:  "{{config.baseUrl}}/api/v1/tickets",
		}},
		Routes: []RouteTmpl{{
			Name: "Email to ticket", EndpointIndex: 0,
			Filter: `{{config.events}}`,
			Map:    MapFeatherDeskTicket,
		}},
		Tester: TesterFeatherDesk,
	}
}

// FeatherDeskTicket is the outbound create-ticket JSON body.
type FeatherDeskTicket struct {
	Subject    string `json:"subject"`
	Requester  string `json:"requester"`
	Body       string `json:"body"`
	MailboxID  string `json:"mailboxId,omitempty"`
	ExternalID string `json:"externalId,omitempty"`
}

// FeatherDeskReply is an inbound ticket-reply webhook payload.
type FeatherDeskReply struct {
	TicketID    string `json:"ticketId"`
	MessageID   string `json:"messageId"`
	InReplyTo   string `json:"inReplyTo"`
	To          string `json:"to"`
	From        string `json:"from"`
	Subject     string `json:"subject"`
	BodyText    string `json:"bodyText"`
	AccountHint string `json:"accountId,omitempty"`
}

// SendRequest is what EmailBridge submit expects from an inbound helpdesk reply.
type SendRequest struct {
	AccountID string            `json:"accountId,omitempty"`
	To        []string          `json:"to"`
	Subject   string            `json:"subject"`
	Text      string            `json:"text"`
	Headers   map[string]string `json:"headers,omitempty"`
}

// MapReplyToSend turns a FeatherDesk reply into a submit send request.
func MapReplyToSend(r FeatherDeskReply) (SendRequest, error) {
	if strings.TrimSpace(r.To) == "" {
		return SendRequest{}, fmt.Errorf("featherdesk: reply missing to")
	}
	if strings.TrimSpace(r.BodyText) == "" && strings.TrimSpace(r.Subject) == "" {
		return SendRequest{}, fmt.Errorf("featherdesk: reply empty")
	}
	hdr := map[string]string{}
	if r.InReplyTo != "" {
		hdr["In-Reply-To"] = r.InReplyTo
		hdr["References"] = r.InReplyTo
	}
	if r.MessageID != "" {
		hdr["X-FeatherDesk-Message-Id"] = r.MessageID
	}
	if r.TicketID != "" {
		hdr["X-FeatherDesk-Ticket-Id"] = r.TicketID
	}
	return SendRequest{
		AccountID: r.AccountHint,
		To:        []string{r.To},
		Subject:   r.Subject,
		Text:      r.BodyText,
		Headers:   hdr,
	}, nil
}

// ParseFeatherDeskReply decodes an inbound callback body.
func ParseFeatherDeskReply(body []byte) (FeatherDeskReply, error) {
	var r FeatherDeskReply
	if err := json.Unmarshal(body, &r); err != nil {
		return FeatherDeskReply{}, err
	}
	return r, nil
}
