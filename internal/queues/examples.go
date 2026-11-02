package queues

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples for the queue page operations (W72, W70 standard). The lint validates each one against the
// generated schema.

const (
	exOrgID     = "org_01m5caqpk0kast63yc13wpy2d9"
	exOrg2ID    = "org_01m5x8b3d0n2c6t4r9y1k7w5qe"
	exAccountID = "acc_01m5cvnpb031g57sqct6mzhq94"
	exEndpoint  = "whe_01m5d9x2r0f4y8j6k3c1v7q2pn"
	exOut1      = "out_01m5f2c8k0q7d3w9x4b6n1t5hz"
	exOut2      = "out_01m5f2d1a0h6s2k8m4p9r3v7wx"
	exDlv1      = "dlv_01m5f3a7c0e9r4t2y6u8i1o3pk"
	exDlv2      = "dlv_01m5f3b2d0g5h7j9k1l3m5n7pq"
	exSys1      = "job_01m5f4c3e0x2z4v6b8n1m3q5wr"
	exJobID     = "job_01m5f5d4f0a1s3d5f7g9h2j4kl"
	exUserID    = "usr_01m5ccvej0dqefkn0xhkcq2q6z"
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func listExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"sending": {Summary: "Sending: a retry-waiting message and one being sent", Value: map[string]any{
			"data": []any{
				map[string]any{
					"id": exOut1, "queue": QueueSending, "kind": ItemOutbound, "organizationId": exOrgID,
					"state": "retrying", "cancellable": true, "accountId": exAccountID,
					"recipient": "anna.rossi@example.com", "recipientCount": 2, "subject": "October invoice",
					"attempts": 2, "nextAttemptAt": "2026-10-27T10:20:00Z", "createdAt": "2026-10-27T09:58:12Z",
				},
				map[string]any{
					"id": exOut2, "queue": QueueSending, "kind": ItemOutbound, "organizationId": exOrgID,
					"state": "sending", "cancellable": false, "notCancellableReason": ReasonInFlight,
					"accountId": exAccountID, "recipient": "billing@example.org", "recipientCount": 1,
					"subject": "Payment reminder", "attempts": 1, "createdAt": "2026-10-27T10:01:40Z",
				},
			},
			"total": 2, "totalCapped": false, "page": 1, "limit": 25,
		}},
		"webhooks": {Summary: "Webhook deliveries: a pending one and a dead letter", Value: map[string]any{
			"data": []any{
				map[string]any{
					"id": exDlv1, "queue": QueueWebhook, "kind": ItemDelivery, "organizationId": exOrgID,
					"state": "pending", "cancellable": true, "accountId": exAccountID, "endpointId": exEndpoint,
					"eventType": "message.received", "attempts": 0, "createdAt": "2026-10-27T10:02:03Z",
				},
				map[string]any{
					"id": exDlv2, "queue": QueueWebhook, "kind": ItemDelivery, "organizationId": exOrgID,
					"state": "deadLettered", "cancellable": true, "accountId": exAccountID, "endpointId": exEndpoint,
					"eventType": "message.sent", "attempts": 8, "createdAt": "2026-10-26T18:30:41Z",
				},
			},
			"total": 2, "totalCapped": false, "page": 1, "limit": 25,
		}},
		"systemMail": {Summary: "System mail (instance owner)", Value: map[string]any{
			"data": []any{
				map[string]any{
					"id": exSys1, "queue": QueueSysmail, "kind": ItemSysmail, "state": "pending",
					"cancellable": true, "recipient": "new.member@example.com", "mailKind": "invitation",
					"attempts": 0, "createdAt": "2026-10-27T10:03:15Z",
				},
			},
			"total": 1, "totalCapped": false, "page": 1, "limit": 25,
		}},
	}
}

func cancelRequestExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"ids": {Summary: "Cancel selected records", Value: map[string]any{"ids": []any{exOut1, exOut2}}},
		"row": {Summary: "Cancel one record (row action)", Value: map[string]any{"ids": []any{exOut1}}},
		"filter": {Summary: "Cancel everything retry-waiting for one account", Value: map[string]any{
			"filter": map[string]any{"state": "retrying", "accountId": exAccountID, "createdFrom": "2026-10-27T00:00:00Z"},
		}},
		"wholeQueueAllOrgs": {Summary: "Instance owner: cancel the whole queue in every organisation", Value: map[string]any{
			"all": true, "scope": ScopeAll, "confirm": QueueSending,
		}},
	}
}

func cancelSyncExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"ids": {Summary: "Cancelled by ids; one was already being sent", Value: map[string]any{
			"cancelled": 1, "skipped": []any{map[string]any{"id": exOut2, "reason": ReasonInFlight}},
		}},
		"nothingMatched": {Summary: "The filter matched no cancellable record (no job)", Value: map[string]any{
			"matched": 0, "cancelled": 0,
		}},
	}
}

func cancelJobExample() map[string]any {
	return map[string]any{
		"job": map[string]any{
			"id": exJobID, "organizationId": exOrgID, "kind": jobKindCancel, "state": "queued",
			"createdBy": exUserID, "createdAt": "2026-10-27T10:05:00Z",
		},
		"matched": 318,
	}
}
