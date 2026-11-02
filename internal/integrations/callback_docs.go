package integrations

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// documentCallback adds the raw inbound callback route to the OpenAPI document (W70). Documentation only.
func documentCallback(srv *httpapi.Server) {
	str := func(desc string) *huma.Schema { return &huma.Schema{Type: huma.TypeString, Description: desc} }
	srv.Document(&huma.Operation{
		OperationID: "integrationCallback", Method: http.MethodPost, Path: "/integrations/{instanceId}/callback",
		Summary: "Inbound helpdesk callback", Tags: []string{"integrations"},
		Description: "Called by the FeatherDesk helpdesk when an agent replies to a ticket: EmailBridge sends the reply as " +
			"an e-mail from the account configured on the instance (or the `accountId` in the body). Public route: " +
			"the credential is an HMAC-SHA256 over `<timestamp>.<body>` with the instance's callback token, sent as " +
			"`X-FeatherDesk-Signature: sha256=<hex>` with `X-FeatherDesk-Timestamp` (Unix seconds, at most 5 minutes " +
			"of clock skew). The same reply (`messageId`, or the body hash) is sent only once, so FeatherDesk may retry. " +
			"At most 60 callbacks per minute per instance; the body is limited to 1 MiB. An unknown or disabled " +
			"instance is `404 not_found`. Answers `204` without a body.",
		Parameters: []*huma.Param{
			{Name: "instanceId", In: "path", Required: true, Description: "Integration instance ID (int_…) of the FeatherDesk connector.",
				Schema: &huma.Schema{Type: huma.TypeString, Pattern: "^int_[0-9a-z]{26}$"}, Example: "int_01m5dzndt02er7qagrrs531p8g"},
			{Name: "X-FeatherDesk-Timestamp", In: "header", Required: true, Description: "Unix time of the signature, in seconds.",
				Schema: &huma.Schema{Type: huma.TypeString}, Example: "1792833262"},
			{Name: "X-FeatherDesk-Signature", In: "header", Required: true,
				Description: "sha256=<hex HMAC-SHA256 of \"<timestamp>.<body>\" with the callback token>.",
				Schema:      &huma.Schema{Type: huma.TypeString},
				Example:     "sha256=5d2f8a1c9b7e4d3f0a6c2b8e1f9d7a4c3b6e0f2a8d1c5b9e7f4a2d6c8b0e3f1a"},
		},
		Security: []map[string][]string{},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {
			Schema: &huma.Schema{Type: huma.TypeObject, Properties: map[string]*huma.Schema{
				"ticketId":  str("Helpdesk ticket ID."),
				"messageId": str("Helpdesk reply ID; deduplicates retries."),
				"inReplyTo": str("Message-ID of the e-mail the reply answers (threading)."),
				"to":        str("Recipient address."),
				"from":      str("Agent address (informational; the account sends)."),
				"subject":   str("Subject of the reply."),
				"bodyText":  str("Plain-text reply (bodyText or subject must be set)."),
				"accountId": str("Account to send from (acc_…) when the instance has none configured."),
			}, Required: []string{"to"}},
			Example: map[string]any{
				"ticketId": "FD-48213", "messageId": "fdmsg_9c41e2b07a", "inReplyTo": "<CAF7x2m9Qp1@mail.example.org>",
				"to": "giulia.bianchi@example.org", "from": "support-agent@example.com",
				"subject": "Re: Your appointment on 4 November", "bodyText": "Hello Giulia,\n\nyour appointment is confirmed.\n\nReception",
			},
		}}},
		Responses: map[string]*huma.Response{"204": {Description: "The reply was queued for sending."}},
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated, Detail: "Invalid callback signature or timestamp."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The integration instance does not exist."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Invalid callback body."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Too many integration callbacks; try again later."}),
		Extensions: httpapi.Phase("P8"),
	})
}
