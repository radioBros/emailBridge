package submit

import "github.com/danielgtaylor/huma/v2"

// OpenAPI documentation examples of the outbound-message routes (W70).

const exampleOutboundID = "out_01m5dsa5x0s3ggtntesyf0cb50"

func jsonExample(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleSubmitRequests() map[string]*huma.Example {
	addr := func(name, address string) map[string]any { return map[string]any{"name": name, "address": address} }
	return map[string]*huma.Example{
		"structured": {Summary: "Structured message with an attachment", Value: map[string]any{
			"from":    addr("Billing", "billing@example.com"),
			"to":      []any{addr("Anna Rossi", "anna.rossi@example.org")},
			"cc":      []any{addr("", "accounts@example.org")},
			"bcc":     []any{addr("", "archive@example.com")},
			"replyTo": []any{addr("Billing support", "support@example.com")},
			"subject": "Invoice 2026-1043",
			"text":    "Hello Anna,\n\nplease find invoice 2026-1043 attached.\n\nKind regards\nBilling team",
			"html":    "<p>Hello Anna,</p><p>please find invoice <b>2026-1043</b> attached.</p><p>Kind regards<br>Billing team</p>",
			"headers": map[string]any{"X-Order-Id": "ORD-58213"},
			"attachments": []any{map[string]any{
				"filename": "invoice-2026-1043.pdf", "contentType": "application/pdf",
				"content": "JVBERi0xLjcKMSAwIG9iago8PCAvVHlwZSAvQ2F0YWxvZyAvUGFnZXMgMiAwIFIgPj4KZW5kb2JqCg==",
			}},
		}},
		"scheduled": {Summary: "Scheduled, at most once, no Sent copy", Value: map[string]any{
			"to":      []any{addr("Marco Bianchi", "marco.bianchi@example.org")},
			"subject": "Your appointment tomorrow at 10:00",
			"text":    "Hello Marco, this is a reminder of your appointment tomorrow at 10:00.",
			"sendAt":  "2026-10-28T08:00:00Z", "policy": "atMostOnce", "maxAttempts": 3,
			"copyToSent": map[string]any{"enabled": false},
		}},
		"raw": {Summary: "Raw RFC 822 with an explicit envelope", Value: map[string]any{
			"raw": "RnJvbTogQmlsbGluZyA8YmlsbGluZ0BleGFtcGxlLmNvbT4NClRvOiBBbm5hIFJvc3NpIDxhbm5hLnJvc3NpQGV4YW1wbGUub3JnPg0KU3ViamVjdDogSW52b2ljZSAyMDI2LTEwNDMNCkRhdGU6IFR1ZSwgMjcgT2N0IDIwMjYgMDk6NTU6MTIgKzAwMDANCk1lc3NhZ2UtSUQ6IDwyMDI2MTAyNzA5NTUxMi40ZjJhQGV4YW1wbGUuY29tPg0KDQpQbGVhc2UgZmluZCB0aGUgaW52b2ljZSBhdHRhY2hlZC4NCg==",
			"envelope": map[string]any{
				"from": "bounces@example.com", "to": []any{"anna.rossi@example.org", "archive@example.com"},
			},
		}},
		"template": {Summary: "From a stored template", Value: map[string]any{
			"to": []any{addr("Anna Rossi", "anna.rossi@example.org")},
			"template": map[string]any{"id": "tpl_01m5djyy00ervzha8qtwwqsg2q", "variables": map[string]any{
				"customer": map[string]any{"firstName": "Anna"},
				"invoice":  map[string]any{"number": "2026-1043", "total": "€ 1,250.00", "issued": "2026-10-27T09:00:00Z"},
			}},
			"listId": "invoices.example.com", "trackClicks": false,
		}},
		"merge": {Summary: "Mail merge (one message per recipient)", Value: map[string]any{
			"template": map[string]any{"id": "tpl_01m5djyy00ervzha8qtwwqsg2q", "version": 2},
			"recipients": []any{
				map[string]any{"to": []any{addr("Anna Rossi", "anna.rossi@example.org")},
					"variables": map[string]any{"customer": map[string]any{"firstName": "Anna"}, "invoice": map[string]any{"number": "2026-1043"}}},
				map[string]any{"to": []any{addr("Marco Bianchi", "marco.bianchi@example.org")},
					"variables": map[string]any{"customer": map[string]any{"firstName": "Marco"}, "invoice": map[string]any{"number": "2026-1044"}}},
			},
		}},
		"reply": {Summary: "Reply to an indexed message", Value: map[string]any{
			"reference": map[string]any{"messageId": "msg_01m5ck6pf04wm1cyx6q10s4x5w", "action": "reply", "inline": true},
			"text":      "Thank you, the invoice has been paid today.",
		}},
		"dryRun": {Summary: "Dry run (validate only)", Value: map[string]any{
			"to":      []any{addr("Anna Rossi", "anna.rossi@example.org")},
			"subject": "Invoice 2026-1043", "text": "Please find the invoice attached.", "dryRun": true,
		}},
	}
}

func exampleEnvelope() map[string]any {
	return map[string]any{
		"from": "billing@example.com", "to": []any{"anna.rossi@example.org"}, "cc": []any{"accounts@example.org"}, "bccCount": 1,
	}
}

func exampleOutboundSent() map[string]any {
	return map[string]any{
		"id": exampleOutboundID, "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "state": "sent", "policy": "standard",
		"messageId": "<20261027101500.1@example.com>", "messageIdHeader": "<20261027101500.1@example.com>",
		"envelope": exampleEnvelope(), "subject": "Invoice 2026-1043", "size": 48990,
		"attempts": []any{
			map[string]any{"at": "2026-10-27T10:15:01Z", "stage": "rcptTo", "code": 451, "enhanced": "4.7.1",
				"response": "451 4.7.1 Greylisted, please try again later", "kind": "temporary", "latencyMs": 812},
			map[string]any{"at": "2026-10-27T10:20:03Z", "stage": "dataEnd", "code": 250, "enhanced": "2.0.0",
				"response": "250 2.0.0 Ok: queued as 4XkLm72Qz1", "kind": "success", "latencyMs": 1430},
		},
		"maxAttempts": 10,
		"copyToSent":  map[string]any{"enabled": true, "path": "Sent", "state": "done"},
		"createdBy":   "key_01m5cqe6d0rs9cyxvy69ynf0t6", "createdAt": "2026-10-27T10:15:00Z",
		"sentAt": "2026-10-27T10:20:04Z", "version": 6,
	}
}

func exampleOutboundScheduled() map[string]any {
	return map[string]any{
		"id": "out_01m5dvdxw09qnk5y0h3w3w2z3d", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "state": "scheduled", "policy": "atMostOnce",
		"messageId": "<20261027101502.2@example.com>", "messageIdHeader": "<20261027101502.2@example.com>",
		"envelope": map[string]any{"from": "billing@example.com", "to": []any{"marco.bianchi@example.org"}},
		"subject":  "Your appointment tomorrow at 10:00", "size": 1210,
		"sendAt": "2026-10-28T08:00:00Z", "nextAttemptAt": "2026-10-28T08:00:00Z", "maxAttempts": 3,
		"copyToSent": map[string]any{"enabled": false, "path": "Sent", "state": "skipped"},
		"createdBy":  "usr_01m5ccvej0dqefkn0xhkcq2q6z", "createdAt": "2026-10-27T10:15:02Z", "version": 1,
	}
}

func exampleOutboundFailed() map[string]any {
	return map[string]any{
		"id": "out_01m5dq6dy0g8x2v5k9n3t7r1wp", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "state": "failed", "policy": "standard",
		"messageId": "<20261027094001.7@example.com>", "messageIdHeader": "<20261027094001.7@example.com>",
		"envelope": map[string]any{"from": "billing@example.com", "to": []any{"unknown.user@example.net"}},
		"subject":  "Invoice 2026-1040", "size": 47702,
		"attempts": []any{map[string]any{
			"at": "2026-10-27T09:40:02Z", "stage": "rcptTo", "code": 550, "enhanced": "5.1.1",
			"response": "550 5.1.1 <unknown.user@example.net>: Recipient address rejected: User unknown",
			"kind":     "permanent", "latencyMs": 640,
			"rejected": []any{map[string]any{"address": "unknown.user@example.net", "code": 550, "enhanced": "5.1.1",
				"message": "Recipient address rejected: User unknown"}},
		}},
		"maxAttempts": 10,
		"copyToSent":  map[string]any{"enabled": true, "path": "Sent", "state": "skipped"},
		"createdBy":   "key_01m5cqe6d0rs9cyxvy69ynf0t6", "createdAt": "2026-10-27T09:40:01Z",
		"finalError": map[string]any{
			"type": "https://emailbridge.dev/problems/smtp_permanent", "title": "SMTP permanent failure", "status": 422,
			"code": "smtp_permanent", "kind": "permanent",
			"detail": "550 5.1.1 <unknown.user@example.net>: Recipient address rejected: User unknown",
		},
		"version": 7,
	}
}

func exampleOutboundList() map[string]any {
	return map[string]any{
		"data":       []any{exampleOutboundScheduled(), exampleOutboundSent()},
		"nextCursor": "eyJpZCI6Im91dF8wMW01ZHNhNXgwczNnZ3RudGVzeWYwY2I1MCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0",
	}
}
