package webhook

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples of the webhook endpoint and delivery operations (W70). Documentation only: the lint
// validates every example against the generated schema.

const (
	exampleOrgID       = "org_01m5caqpk0kast63yc13wpy2d9"
	exampleAccountID   = "acc_01m5cvnpb031g57sqct6mzhq94"
	exampleEndpointID  = "whe_01m5cshyc0d23xkhrbz7e490pm"
	exampleEndpoint2ID = "whe_01m5dgv610p98z84d29j5x3pmt"
	exampleDeliveryID  = "dlv_01m5dvdxw0j7a55tjavm1fmhex"
	exampleDelivery2ID = "dlv_01m5dsa5x0v78ag81yce7hjpr3"
	exampleEventID     = "evt_01m5czx69032gsbkp82n4ajd1w"
	exampleRouteID     = "rte_01m5dafy40qhsm3b2qx0ef6skh"
	exampleJobID       = "job_01m5cnaee0w96fc46z350nc6q0"
	exampleUserID      = "usr_01m5ccvej0dqefkn0xhkcq2q6z"
	exampleSecret      = "whsec_6Q2rT9vK1mX8pL4nB7cZ3dF0hJ5sA2wE1yR4uI8oP0s=" //nolint:gosec // G101: documentation example webhook secret, not a real credential.
	exampleFilterCEL   = `type == "message.received" && fromDomain() == "example.org"`
	exampleMapCEL      = `{"messageId": data.messageId, "subject": subject(), "from": from()}`
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleEndpointView() map[string]any {
	return map[string]any{ //nolint:gosec // G101: documentation example endpoint view, not real credentials.
		"id": exampleEndpointID, "organizationId": exampleOrgID, "name": "CRM inbound", "kind": "http",
		"enabled": true, "url": "https://crm.example.com/hooks/emailbridge",
		"events": []any{"message.received", "message.sent", "message.bounced"},
		"filter": map[string]any{"accountIds": []any{exampleAccountID}, "mailboxPaths": []any{"INBOX"}},
		"headers": []any{
			map[string]any{"name": "X-Api-Key", "secret": true, "hasValue": true},
			map[string]any{"name": "X-Tenant", "value": "acme-legal"},
		},
		"payload":       map[string]any{"text": "plain", "textMax": 65536, "attachments": true, "replyText": true},
		"secretPreview": "whsec_…oP0s=",
		"security":      map[string]any{"signatureMode": "standard"},
		"retry":         map[string]any{"maxAttempts": 10, "backoffBase": "5s", "backoffJitter": 0.2, "timeout": "30s"},
		"delivery": map[string]any{"ordering": "strict", "maxConcurrency": 8, "contentType": "json",
			"retryOn4xx": false, "autoDisableAfter": "120h0m0s"},
		"breaker":      map[string]any{"state": "closed", "failures": 0},
		"lastDelivery": map[string]any{"state": "succeeded", "statusCode": 200, "at": "2026-10-27T09:14:23Z"},
		"createdAt":    "2026-10-20T10:30:00Z", "updatedAt": "2026-10-26T14:02:51Z", "version": 5,
	}
}

func exampleRuleEndpointView() map[string]any {
	return map[string]any{ //nolint:gosec // G101: documentation example endpoint view, not real credentials.
		"id": exampleEndpoint2ID, "organizationId": exampleOrgID, "name": "Helpdesk tickets", "kind": "http",
		"enabled": false, "url": "https://helpdesk.example.net/api/inbound-mail",
		"events":         []any{"message.received"},
		"secretPreview":  "whsec_…T6g=",
		"security":       map[string]any{"signatureMode": "bodyOnly"},
		"retry":          map[string]any{"maxAttempts": 5, "backoffBase": "10s", "backoffJitter": 0.2, "timeout": "15s"},
		"delivery":       map[string]any{"ordering": "bestEffort", "maxConcurrency": 4, "contentType": "json", "retryOn4xx": false, "autoDisableAfter": "72h0m0s"},
		"disabledReason": map[string]any{"code": "continuous_failure", "at": "2026-10-26T03:12:40Z"},
		"breaker":        map[string]any{"state": "open", "openedAt": "2026-10-26T02:58:10Z", "failures": 12},
		"rule":           map[string]any{"filter": exampleFilterCEL, "map": exampleMapCEL, "currentVersion": 2},
		"lastDelivery":   map[string]any{"state": "deadLettered", "statusCode": 503, "at": "2026-10-26T03:12:39Z"},
		"createdAt":      "2026-10-22T08:15:00Z", "updatedAt": "2026-10-26T03:12:40Z", "version": 9,
	}
}

func exampleEndpointList() map[string]any {
	return map[string]any{
		"data":       []any{exampleEndpointView(), exampleRuleEndpointView()},
		"nextCursor": "eyJpZCI6IndoZV8wMW01ZGd2NjEwcDk4ejg0ZDI5ajV4M3BtdCJ9",
	}
}

func createEndpointExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"http": {Summary: "HTTPS endpoint for selected events",
			Value: map[string]any{
				"name": "CRM inbound", "kind": "http", "url": "https://crm.example.com/hooks/emailbridge",
				"events": []any{"message.received", "message.sent", "message.bounced"},
				"filter": map[string]any{"accountIds": []any{exampleAccountID}, "mailboxPaths": []any{"INBOX"}},
				"headers": []any{
					map[string]any{"name": "X-Api-Key", "value": "crm-ingest-7f3c2b9e", "secret": true},
					map[string]any{"name": "X-Tenant", "value": "acme-legal"},
				},
				"payload": map[string]any{"text": "plain", "attachments": true, "replyText": true},
				"retry":   map[string]any{"maxAttempts": 10, "backoffBase": "5s", "backoffJitter": 0.2, "timeout": "30s"},
			}},
		"rule": {Summary: "Endpoint with a CEL rule that filters and reshapes the body",
			Value: map[string]any{
				"name": "Helpdesk tickets", "kind": "http", "url": "https://helpdesk.example.net/api/inbound-mail",
				"events":   []any{"message.received"},
				"secret":   exampleSecret,
				"security": map[string]any{"signatureMode": "bodyOnly"},
				"delivery": map[string]any{"ordering": "bestEffort", "maxConcurrency": 4},
				"rule":     map[string]any{"filter": exampleFilterCEL, "map": exampleMapCEL},
			}},
		"mtls": {Summary: "Endpoint behind mutual TLS (webhook.mtls switch)",
			Value: map[string]any{
				"name": "Archive service", "kind": "http", "url": "https://archive.example.org/ingest",
				"events": []any{"*"},
				"security": map[string]any{"signatureMode": "standard", "tls": map[string]any{
					"clientCert": "-----BEGIN CERTIFICATE-----\nMIIBszCCAVmgAwIBAgIUQ2…\n-----END CERTIFICATE-----\n",
					"clientKey":  "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49…\n-----END PRIVATE KEY-----\n",
					"caBundle":   "-----BEGIN CERTIFICATE-----\nMIIBdTCCARugAwIBAgIUY7…\n-----END CERTIFICATE-----\n",
				}},
			}},
	}
}

func patchEndpointExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"reenable": {Summary: "Re-enable after fixing the receiver", Value: map[string]any{"enabled": true}},
		"events": {Summary: "Change events and payload",
			Value: map[string]any{
				"events":  []any{"message.received", "message.updated"},
				"payload": map[string]any{"text": "both", "textMax": 32768, "headers": []any{"list-id", "x-priority"}},
			}},
		"rule": {Summary: "Replace the rule (creates a rule version)",
			Value: map[string]any{"rule": map[string]any{
				"filter": `type == "message.received" && isInbox() && !isAutoReply()`, "map": exampleMapCEL}}},
	}
}

func exampleAttemptOK() map[string]any {
	return map[string]any{"at": "2026-10-27T09:40:00Z", "statusCode": 200, "latencyMs": 184,
		"responseExcerpt": `{"ok":true}`, "responseHeaders": map[string]any{"Content-Type": "application/json"}}
}

func exampleDelivery() map[string]any {
	return map[string]any{
		"id": exampleDeliveryID, "organizationId": exampleOrgID, "accountId": exampleAccountID,
		"eventId": exampleEventID, "eventType": "message.received", "endpointId": exampleEndpointID,
		"state": "succeeded",
		"attempts": []any{
			map[string]any{"at": "2026-10-27T09:14:22Z", "statusCode": 503, "latencyMs": 2140, "kind": "temporary",
				"error": "webhook_http_error", "responseExcerpt": "upstream unavailable",
				"responseHeaders": map[string]any{"Retry-After": "5", "Content-Type": "text/plain"}},
			map[string]any{"at": "2026-10-27T09:14:28Z", "statusCode": 200, "latencyMs": 184,
				"responseExcerpt": `{"ok":true}`, "responseHeaders": map[string]any{"Content-Type": "application/json"}},
		},
		"lastAttemptAt": "2026-10-27T09:14:28Z", "createdAt": "2026-10-27T09:14:22Z", "expireAt": "2026-11-26T09:14:22Z",
	}
}

func exampleDeadDelivery() map[string]any {
	return map[string]any{
		"id": exampleDelivery2ID, "organizationId": exampleOrgID, "accountId": exampleAccountID,
		"eventId": "evt_01m5d8c6505c3fnkwbkknhkq61", "eventType": "message.sent", "endpointId": exampleEndpoint2ID,
		"routeId": exampleRouteID, "state": "deadLettered",
		"attempts": []any{
			map[string]any{"at": "2026-10-26T03:12:39Z", "latencyMs": 15000, "kind": "temporary", "error": "webhook_timeout"},
		},
		"lastAttemptAt": "2026-10-26T03:12:39Z", "createdAt": "2026-10-25T21:40:02Z", "expireAt": "2026-11-24T21:40:02Z",
	}
}

func exampleReplayDelivery() map[string]any {
	d := exampleDeadDelivery()
	d["id"], d["state"], d["replayOf"] = "dlv_01m5e1s5s0fzzsp861d0hb22qd", "pending", exampleDelivery2ID
	d["attempts"] = []any{}
	delete(d, "lastAttemptAt")
	d["createdAt"], d["expireAt"] = "2026-10-27T10:00:00Z", "2026-11-26T10:00:00Z"
	return d
}

func exampleDeliveryList() map[string]any {
	return map[string]any{
		"data":       []any{exampleDelivery(), exampleDeadDelivery()},
		"nextCursor": "eyJpZCI6ImRsdl8wMW01ZHNhNXgwdjc4YWc4MXljZTdoanByMyJ9",
	}
}

func exampleJobAccepted(kind string) map[string]any {
	return map[string]any{"job": map[string]any{
		"id": exampleJobID, "organizationId": exampleOrgID, "kind": kind, "state": "queued",
		"createdBy": exampleUserID, "createdAt": "2026-10-27T10:00:00Z",
	}}
}

func bulkReplayExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"filter": {Summary: "Dead-lettered deliveries of one endpoint in a time range",
			Value: map[string]any{"endpointId": exampleEndpoint2ID, "state": "deadLettered",
				"from": "2026-10-26T00:00:00Z", "to": "2026-10-27T00:00:00Z"}},
		"ids": {Summary: "Selected deliveries",
			Value: map[string]any{"ids": []any{exampleDelivery2ID, "dlv_01m5dq6dy0bpw4pexpdp4cjcn0"}}},
	}
}

func exampleEventsReplay() map[string]any {
	return map[string]any{"endpointId": exampleEndpointID, "from": "2026-10-26T00:00:00Z", "to": "2026-10-27T00:00:00Z",
		"types": []any{"message.received"}, "ignoreEndpointFilter": false}
}

func ruleTestExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"sample": {Summary: "Built-in sample event", Value: map[string]any{"sample": "message.received"}},
		"stored": {Summary: "A stored event", Value: map[string]any{"eventId": exampleEventID}},
		"draft": {Summary: "Draft rule against a sample, before saving",
			Value: map[string]any{"sample": "message.received", "filter": exampleFilterCEL, "map": exampleMapCEL}},
		"event": {Summary: "Inline event",
			Value: map[string]any{"event": map[string]any{
				"specversion": "1.0", "id": exampleEventID, "type": "message.received",
				"source":  "/organizations/" + exampleOrgID + "/accounts/" + exampleAccountID,
				"subject": "msg_01m5ch2yg09swc93g54trswp15", "time": "2026-10-27T09:14:22.318Z",
				"data": map[string]any{"accountId": exampleAccountID, "messageId": "msg_01m5ch2yg09swc93g54trswp15",
					"mailbox": map[string]any{"mailboxId": "mbx_01m5ccvej0khs793a5tesr1an7", "path": "INBOX"},
					"from":    map[string]any{"name": "Giulia Bianchi", "address": "giulia.bianchi@example.org"},
					"subject": "Re: Your appointment on 4 November"},
			}}},
	}
}

func ruleTestResultExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"matched": {Summary: "Matched, with the mapped body",
			Value: map[string]any{"matched": true, "costUsed": 38, "cost": 38, "durationMs": 1,
				"mapped": map[string]any{"messageId": "msg_01m5ch2yg09swc93g54trswp15",
					"subject": "Re: Your appointment on 4 November", "from": "giulia.bianchi@example.org"},
				"trace": []any{
					map[string]any{"expr": `type == "message.received"`, "result": true},
					map[string]any{"expr": `fromDomain() == "example.org"`, "result": true},
				}}},
		"compileError": {Summary: "Draft that does not compile",
			Value: map[string]any{"matched": false, "costUsed": 0, "cost": 0, "durationMs": 0,
				"error":   "ERROR: <input>:1:31: undeclared reference to 'fromDomian'",
				"errorAt": map[string]any{"path": "filter", "line": 1, "column": 31}}},
	}
}

func exampleRuleVersions() map[string]any {
	return map[string]any{
		"data": []any{
			map[string]any{"id": "erv_01m5dxhnv019njbyyapkhp5rev", "organizationId": exampleOrgID, "endpointId": exampleEndpoint2ID,
				"version": 2, "filter": exampleFilterCEL, "map": exampleMapCEL, "createdBy": exampleUserID,
				"createdAt": "2026-10-26T14:02:51Z"},
			map[string]any{"id": "erv_01m5dvdxw0j7a55tjavm1fmhex", "organizationId": exampleOrgID, "endpointId": exampleEndpoint2ID,
				"version": 1, "filter": `type == "message.received"`,
				"builder":   map[string]any{"match": "all", "conditions": []any{map[string]any{"field": "type", "op": "eq", "value": "message.received"}}},
				"createdBy": exampleUserID, "createdAt": "2026-10-22T08:15:00Z"},
		},
		"total": 2,
	}
}

func exampleDryRunRequest() map[string]any {
	return map[string]any{"windowHours": 24, "limit": 500, "filter": exampleFilterCEL, "map": exampleMapCEL}
}
