package gateways

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

func (m *Module) registerAPI(server *httpapi.Server) {
	phase := httpapi.Phase("P5")
	httpapi.Register(server, huma.Operation{
		OperationID: "listSmtpGateways", Method: http.MethodGet, Path: "/v1/smtp-gateways",
		Summary: "List SMTP gateways", Tags: []string{"smtp-gateways"},
		Description: "Lists the organisation's sending gateways: SMTP relays and API providers (SES, Postmark, Mailgun, " +
			"SendGrid, Brevo) that accounts can send through instead of their own SMTP server (`smtp.gatewayId` on the " +
			"account). Passwords and API secrets are never returned; `auth.hasPassword` / `credentials.has…` say whether " +
			"they are stored. Requires the `read` permission (every role; API keys `gateways:read`). `q` matches part of " +
			"the name. Pagination: `limit` (default 50, max 200) with `cursor`, or `page` with `includeTotal=true`. Sort " +
			"`name`, `createdAt` or `id` (prefix `-` for descending; default `-createdAt`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeSortNotAllowed, errs.CodeValidationFailed),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleGatewayList())}},
		Extensions: httpapi.WithScope(phase, "gateways:read"),
	}, m.list)
	httpapi.Register(server, huma.Operation{
		OperationID: "createSmtpGateway", Method: http.MethodPost, Path: "/v1/smtp-gateways",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create SMTP gateway", Tags: []string{"smtp-gateways"},
		Description: "Creates a sending gateway. `kind: smtp` (default) needs `host`, `port`, `tls` and `auth`; the API " +
			"kinds `ses`, `postmark`, `mailgun`, `sendgrid` and `brevo` need `credentials` (and `api.domain` for mailgun) " +
			"and must not carry SMTP fields. Secrets are encrypted at rest and never returned. For API gateways the " +
			"response carries `webhookUrl`: configure it at the provider so delivery, bounce and complaint events reach " +
			"EmailBridge. Names are unique (`409 gateway_name_taken`); the kind cannot change later. Requires " +
			"`outbound.write` (owner, admin, operator; API keys `outbound:write`). Writes a `smtpGateway.created` audit " +
			"record. Supports `Idempotency-Key`; returns `Location` and `ETag`.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeValidationFailed, errs.CodeGatewayNameTaken, errs.CodeIdempotencyKeyReused),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"smtpRelay": {Summary: "SMTP relay", Value: map[string]any{
				"name": "Office relay", "host": "relay.example.com", "port": 587, "tls": "starttls",
				"auth":   map[string]any{"user": "relay@example.com", "pass": "relay-password-2026"},
				"policy": "standard", "maxAttempts": 5, "ehloName": "relay-out.example.com",
			}},
			"ses": {Summary: "Amazon SES (API)", Value: map[string]any{
				"name": "Amazon SES eu-west-1", "kind": "ses",
				"api": map[string]any{"region": "eu-west-1", "extra": map[string]any{"configurationSet": "emailbridge-prod"}},
				//nolint:gosec // G101: documentation example credentials, not real secrets.
				"credentials": map[string]any{
					"accessKey": "AKIAIOSFODNN7EXAMPLE", "secretKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
				},
			}},
			"mailgun": {Summary: "Mailgun (API, EU region)", Value: map[string]any{
				"name": "Mailgun EU", "kind": "mailgun",
				"api": map[string]any{"region": "eu", "domain": "mg.example.com"},
				//nolint:gosec // G101: documentation example credentials, not real secrets.
				"credentials": map[string]any{"apiKey": "key-3ax6xnjp29jd6fds4gc373sgvjxteol0", "webhookSecret": "whsec_4f8Kq2mV7tZ1xR9pW3bN6cL0"},
			}},
			"postmark": {Summary: "Postmark (API)", Value: map[string]any{
				"name": "Postmark transactional", "kind": "postmark",
				"api": map[string]any{"extra": map[string]any{"messageStream": "outbound"}},
				//nolint:gosec // G101: documentation example credentials, not real secrets.
				"credentials": map[string]any{"apiKey": "c5f1e2a3-7b4d-4c8e-9f60-1a2b3c4d5e6f", "webhookSecret": "pm-hook-9Tq2xV7mK4"},
			}},
		})},
		Responses:  map[string]*huma.Response{"201": {Content: jsonContent(exampleSMTPGateway())}},
		Extensions: httpapi.WithScope(phase, "outbound:write"),
	}, m.create)
	httpapi.Register(server, huma.Operation{
		OperationID: "getSmtpGateway", Method: http.MethodGet, Path: "/v1/smtp-gateways/{gatewayId}",
		Summary: "Get SMTP gateway", Tags: []string{"smtp-gateways"},
		Description: "Returns one gateway without its secrets, with `webhookUrl` for API gateways and its `ETag`. Requires " +
			"the `read` permission (API keys `gateways:read`). Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound),
		Responses: map[string]*huma.Response{"200": {Content: jsonExamples(map[string]*huma.Example{
			"smtp": {Summary: "SMTP relay", Value: exampleSMTPGateway()},
			"api":  {Summary: "Mailgun API gateway", Value: exampleAPIGateway()},
		})}},
		Extensions: httpapi.WithScope(phase, "gateways:read"),
	}, m.get)
	httpapi.Register(server, huma.Operation{
		OperationID: "patchSmtpGateway", Method: http.MethodPatch, Path: "/v1/smtp-gateways/{gatewayId}",
		Summary: "Update SMTP gateway", Tags: []string{"smtp-gateways"},
		Description: "Changes a gateway; omitted fields stay unchanged, `auth.pass` and `credentials.*` replace only the " +
			"secrets sent. `enabled: false` stops sends through the gateway. The kind cannot change " +
			"(`409 gateway_kind_immutable`). Accounts using the gateway pick up the new settings on their next send. " +
			"Requires `outbound.write` (API keys `outbound:write`). Send the last `ETag` as `If-Match` " +
			"(`412 version_mismatch`). Writes a `smtpGateway.updated` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeValidationFailed, errs.CodeGatewayNameTaken, errs.CodeGatewayKindImmutable,
			errs.CodeVersionMismatch),
		RequestBody: &huma.RequestBody{Content: jsonExamples(map[string]*huma.Example{
			"rotatePassword": {Summary: "Rotate the relay password", Value: map[string]any{
				"auth": map[string]any{"user": "relay@example.com", "pass": "relay-password-2027"},
			}},
			"disable": {Summary: "Disable the gateway", Value: map[string]any{"enabled": false}},
		})},
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleSMTPGateway())}},
		Extensions: httpapi.WithScope(phase, "outbound:write"),
	}, m.patch)
	httpapi.Register(server, huma.Operation{
		OperationID: "deleteSmtpGateway", Method: http.MethodDelete, Path: "/v1/smtp-gateways/{gatewayId}",
		Summary: "Delete SMTP gateway", Tags: []string{"smtp-gateways"},
		Description: "Deletes a gateway and its encrypted secrets; answers `204`. Accounts whose `smtp.gatewayId` still " +
			"points to it are refused with `409 smtp_gateway_in_use` (detail includes `count`): clear the binding first. " +
			"Requires `outbound.write` (API keys `outbound:write`); `If-Match` is honoured. Writes a " +
			"`smtpGateway.deleted` audit record.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
			errs.CodeNotFound, errs.CodeSMTPGatewayInUse, errs.CodeVersionMismatch),
		Extensions: httpapi.WithScope(phase, "outbound:write"),
	}, m.delete)
	httpapi.Register(server, huma.Operation{
		OperationID: "verifySmtpGateway", Method: http.MethodPost, Path: "/v1/smtp-gateways/{gatewayId}/verify",
		Summary: "Verify SMTP gateway", Tags: []string{"smtp-gateways"},
		Description: "Connects to an SMTP gateway with its stored settings, says EHLO, negotiates TLS and logs in, then " +
			"disconnects; no mail is sent. The answer is `200` with `ok: false`, `code` and `detail` when the login " +
			"fails. API gateways cannot be verified this way (`422 validation_failed`). Limited to 5 requests per minute " +
			"per organisation (`429 rate_limited`). Requires `outbound.write` (API keys `outbound:write`). Writes a " +
			"`smtpGateway.verified` audit record. No request body.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated}, httpapi.ProblemDoc{Code: errs.CodeForbidden},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing}, httpapi.ProblemDoc{Code: errs.CodeNotFound},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Verify is only supported for SMTP gateways."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Gateway verify is limited to 5 requests per minute per organisation."}),
		Responses: map[string]*huma.Response{"200": {Content: jsonExamples(map[string]*huma.Example{
			"ok": {Summary: "Login succeeded", Value: map[string]any{
				"ok": true, "latencyMs": 356, "capabilities": []any{"PIPELINING", "8BITMIME", "SMTPUTF8", "SIZE", "AUTH"},
			}},
			"authFailed": {Summary: "Login rejected", Value: map[string]any{
				"ok": false, "latencyMs": 1210, "code": "mail_auth_failed", "detail": "535 5.7.8 Authentication credentials invalid",
			}},
		})}},
		Extensions: httpapi.WithScope(phase, "outbound:write"),
	}, m.verify)
	server.Document(&huma.Operation{
		OperationID: "receiveGatewayWebhook", Method: http.MethodPost, Path: "/callbacks/gateways/{gatewayId}",
		Summary: "Receive provider delivery webhook", Tags: []string{"smtp-gateways"},
		Description: "Public endpoint the API providers call with delivery events (the gateway's " +
			"`webhookUrl`). Authenticated per provider with the gateway's `webhookSecret`: Mailgun HMAC signature in the " +
			"body, SendGrid signed-event headers, Postmark and Brevo `?token=` (Brevo also a bearer token or " +
			"`X-Brevo-Signature`), SES signed SNS messages (subscription confirmations are confirmed automatically). " +
			"Hard bounces and complaints put the recipient on the organisation's suppression list and emit " +
			"`message.bounced` / `message.complained` for the outbound message they belong to. Body ≤ 1 MiB, 60 " +
			"requests per minute per gateway. Unknown, disabled or SMTP gateways are `404`; a bad signature is `401`.",
		Parameters: []*huma.Param{
			{Name: "gatewayId", In: "path", Required: true, Description: "API gateway ID (gwy_…).",
				Schema: &huma.Schema{Type: "string"}, Example: "gwy_01m56nt9y6czt3tgr4yj3zpebf"},
			{Name: "token", In: "query", Description: "Postmark / Brevo: the gateway's webhook secret.",
				Schema: &huma.Schema{Type: "string"}, Example: "pm-hook-9Tq2xV7mK4"},
		},
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {
			Schema: &huma.Schema{Type: "object", Description: "The provider's own webhook payload, unchanged."},
			Examples: map[string]*huma.Example{
				"postmarkBounce": {Summary: "Postmark bounce", Value: map[string]any{
					"RecordType": "Bounce", "Type": "HardBounce", "MessageID": "883953f4-6105-42a2-a16a-77a8eac79483",
					"Email": "unknown.user@example.org", "BouncedAt": "2026-10-27T10:02:11Z",
					"Description": "The server was unable to deliver your message (ex: unknown user, mailbox not found).",
				}},
				"mailgunDelivered": {Summary: "Mailgun delivered", Value: map[string]any{
					//nolint:gosec // G101: documentation example webhook signature, not a real secret.
					"signature": map[string]any{
						"timestamp": "1793095331", "token": "a8ce0edb2dd8301dee6c2405235584e45aa91d1e9f979f3de0",
						"signature": "d2271d12299f6592d9d44cd9d250f0704e4674c30d79d07c47a66f95ce71cf55",
					},
					"event-data": map[string]any{
						"event": "delivered", "recipient": "customer@example.org",
						"message": map[string]any{"headers": map[string]any{"message-id": "20261027100211.1.ABCDEF@mg.example.com"}},
					},
				}},
			},
		}}},
		Security:  []map[string][]string{},
		Responses: map[string]*huma.Response{"204": {Description: "Accepted (also for payloads without events)."}},
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Invalid webhook body."},
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated, Detail: "Unauthorized."},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "Not found."},
			httpapi.ProblemDoc{Code: errs.CodePayloadTooLarge, Detail: "Request body too large."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Too many gateway callbacks; try again later."},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "Delivery sink is not configured."}),
		Extensions: httpapi.Phase("P8"),
	})
}

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleSMTPGateway() map[string]any {
	return map[string]any{
		"id": "gwy_01m5t5qewav4xa80hsn73zwes3", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "Office relay", "kind": "smtp", "host": "relay.example.com", "port": 587, "tls": "starttls",
		"auth":     map[string]any{"user": "relay@example.com", "hasPassword": true},
		"ehloName": "relay-out.example.com", "policy": "standard", "maxAttempts": 5, "enabled": true,
		"createdAt": "2026-10-10T09:00:00Z", "updatedAt": "2026-10-25T13:44:02Z", "version": 2,
	}
}

func exampleAPIGateway() map[string]any {
	return map[string]any{
		"id": "gwy_01m56nt9y6czt3tgr4yj3zpebf", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "Mailgun EU", "kind": "mailgun",
		"api": map[string]any{"region": "eu", "domain": "mg.example.com"},
		"credentials": map[string]any{
			"hasAccessKey": false, "hasSecretKey": false, "hasApiKey": true, "hasWebhookSecret": true,
		},
		"webhookUrl": "https://emailbridge.example.com/callbacks/gateways/gwy_01m56nt9y6czt3tgr4yj3zpebf",
		"enabled":    true, "createdAt": "2026-10-12T15:20:00Z", "updatedAt": "2026-10-12T15:20:00Z", "version": 1,
	}
}

func exampleGatewayList() map[string]any {
	return map[string]any{
		"data":       []any{exampleSMTPGateway(), exampleAPIGateway()},
		"nextCursor": "eyJpZCI6Imd3eV8wMW01Nm50OXk2Y3p0M3RncjR5ajN6cGViZiIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0",
	}
}

type listIn struct {
	Limit        int    `query:"limit" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6Imd3eV8wMW01Nm50OXk2Y3p0M3RncjR5ajN6cGViZiIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"`
	Page         int    `query:"page" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool   `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string `query:"sort" doc:"name, createdAt or id; prefix - for descending. Default -createdAt." example:"name"`
	Q            string `query:"q" doc:"Case-insensitive part of the gateway name (≤ 200 characters)." example:"relay"`
}

type listOut struct {
	Body List
}

type createIn struct {
	Body CreateInput
}

type createOut struct {
	Status   int
	Location string `header:"Location" doc:"URL of the new gateway." example:"/v1/smtp-gateways/gwy_01m5t5qewav4xa80hsn73zwes3"`
	ETag     string `header:"ETag" doc:"Version of the new gateway." example:"\"v1\""`
	Body     View
}

type idIn struct {
	GatewayID string `path:"gatewayId" doc:"SMTP gateway ID (gwy_…)." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
}

type getOut struct {
	ETag string `header:"ETag" doc:"Current version; send as If-Match on PATCH / DELETE." example:"\"v2\""`
	Body View
}

type patchIn struct {
	GatewayID string `path:"gatewayId" doc:"SMTP gateway ID (gwy_…)." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
	IfMatch   string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v2\""`
	Body      PatchInput
}

type deleteIn struct {
	GatewayID string `path:"gatewayId" doc:"SMTP gateway ID (gwy_…)." example:"gwy_01m5t5qewav4xa80hsn73zwes3"`
	IfMatch   string `header:"If-Match" doc:"ETag from the last read; a stale value fails with 412 version_mismatch. Omit to skip the check." example:"\"v2\""`
}

type verifyOut struct {
	Body VerifyResult
}

func (m *Module) list(ctx context.Context, in *listIn) (*listOut, error) {
	res, err := m.svc.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Q: in.Q,
	})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: *res}, nil
}

func (m *Module) create(ctx context.Context, in *createIn) (*createOut, error) {
	g, err := m.svc.Create(ctx, in.Body)
	if err != nil {
		return nil, err
	}
	return &createOut{
		Status: http.StatusCreated, Location: "/v1/smtp-gateways/" + g.ID,
		ETag: formatETag(g.Version), Body: m.svc.ViewOf(g),
	}, nil
}

func (m *Module) get(ctx context.Context, in *idIn) (*getOut, error) {
	g, err := m.svc.Get(ctx, in.GatewayID)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(g.Version), Body: m.svc.ViewOf(g)}, nil
}

func (m *Module) patch(ctx context.Context, in *patchIn) (*getOut, error) {
	g, err := m.svc.Patch(ctx, in.GatewayID, in.IfMatch, in.Body)
	if err != nil {
		return nil, err
	}
	return &getOut{ETag: formatETag(g.Version), Body: m.svc.ViewOf(g)}, nil
}

func (m *Module) delete(ctx context.Context, in *deleteIn) (*struct{}, error) {
	if err := m.svc.Delete(ctx, in.GatewayID, in.IfMatch); err != nil {
		return nil, err
	}
	return &struct{}{}, nil
}

func (m *Module) verify(ctx context.Context, in *idIn) (*verifyOut, error) {
	res, err := m.svc.Verify(ctx, in.GatewayID)
	if err != nil {
		return nil, err
	}
	return &verifyOut{Body: *res}, nil
}
