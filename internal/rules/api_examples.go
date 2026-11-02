package rules

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples of the route, builder and named-list operations (W70). Documentation only: the lint
// validates every example against the generated schema.

const (
	exampleOrgID      = "org_01m5caqpk0kast63yc13wpy2d9"
	exampleRouteID    = "rte_01m5dafy40qhsm3b2qx0ef6skh"
	exampleRoute2ID   = "rte_01m5dckp30mdexhj4qp140dym4"
	exampleEndpointID = "whe_01m5cshyc0d23xkhrbz7e490pm"
	exampleEventID    = "evt_01m5czx69032gsbkp82n4ajd1w"
	exampleUserID     = "usr_01m5ccvej0dqefkn0xhkcq2q6z"
	exampleJobID      = "job_01m5cnaee0w96fc46z350nc6q0"
	exampleListID     = "rls_01m5dn2nz0q7h3ncd1j49hxtnb"
	exampleCompiled   = `(type == "message.received") && (domain(data.from) in ["example.org", "example.net"]) && (hasAttachments())`
	exampleMap        = `{"messageId": data.messageId, "from": from(), "subject": subject()}`
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleBuilder() map[string]any {
	return map[string]any{"match": "all", "conditions": []any{
		map[string]any{"field": "type", "op": "eq", "value": "message.received"},
		map[string]any{"field": "from.domain", "op": "in", "value": []any{"example.org", "example.net"}},
		map[string]any{"field": "hasAttachments", "op": "eq", "value": true},
	}}
}

func exampleRoute() map[string]any {
	return map[string]any{
		"id": exampleRouteID, "organizationId": exampleOrgID, "name": "Invoices to accounting", "enabled": true,
		"endpointId": exampleEndpointID, "currentVersion": 3, "filter": exampleCompiled, "map": exampleMap,
		"builder": exampleBuilder(), "headers": map[string]any{"X-Queue": "accounting"},
		"payload": map[string]any{"attachments": true}, "order": 10,
		"createdAt": "2026-10-21T08:00:00Z", "updatedAt": "2026-10-26T14:20:05Z", "version": 4,
	}
}

func exampleRoute2() map[string]any {
	return map[string]any{
		"id": exampleRoute2ID, "organizationId": exampleOrgID, "name": "Bounces to CRM", "enabled": true,
		"endpointId": "whe_01m5dgv610p98z84d29j5x3pmt", "currentVersion": 1,
		"filter": `type == "message.bounced" || type == "message.complained"`, "order": 20,
		"lastError": map[string]any{"code": "map", "message": "no such key: recipient", "at": "2026-10-25T17:02:11Z"},
		"createdAt": "2026-10-22T09:10:00Z", "updatedAt": "2026-10-22T09:10:00Z", "version": 1,
	}
}

func exampleRouteList() map[string]any {
	return map[string]any{"data": []any{exampleRoute(), exampleRoute2()},
		"nextCursor": "eyJpZCI6InJ0ZV8wMW01ZGNrcDMwbWRleGhqNHFwMTQwZHltNCJ9"}
}

func createRouteExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"cel": {Summary: "CEL filter and map", Value: map[string]any{
			"name": "Invoices to accounting", "endpointId": exampleEndpointID,
			"filter": `type == "message.received" && isInbox() && hasAttachment("pdf") && subjectContains("invoice")`,
			"map":    exampleMap, "order": 10, "headers": map[string]any{"X-Queue": "accounting"},
		}},
		"builder": {Summary: "No-code builder (compiled to the filter)", Value: map[string]any{
			"name": "Customer mail with attachments", "endpointId": exampleEndpointID, "builder": exampleBuilder(),
		}},
	}
}

func patchRouteExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"disable": {Summary: "Disable", Value: map[string]any{"enabled": false}},
		"filter": {Summary: "New filter (creates version 4)",
			Value: map[string]any{"filter": `type == "message.received" && hasAttachment("pdf") && !isAutoReply()`}},
	}
}

func testRouteExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"sample": {Summary: "Built-in sample event", Value: map[string]any{"sample": "message.received"}},
		"stored": {Summary: "A stored event", Value: map[string]any{"eventId": exampleEventID}},
		"draft": {Summary: "Draft builder against a sample, before saving",
			Value: map[string]any{"sample": "message.received", "builder": exampleBuilder()}},
		"event": {Summary: "Inline event", Value: map[string]any{"event": map[string]any{
			"specversion": "1.0", "id": exampleEventID, "type": "message.received",
			"source":  "/organizations/" + exampleOrgID + "/accounts/acc_01m5cvnpb031g57sqct6mzhq94",
			"subject": "msg_01m5ch2yg09swc93g54trswp15", "time": "2026-10-27T09:14:22.318Z",
			"data": map[string]any{"messageId": "msg_01m5ch2yg09swc93g54trswp15",
				"mailbox": map[string]any{"path": "INBOX", "specialUse": "\\Inbox"},
				"from":    map[string]any{"name": "Giulia Bianchi", "address": "giulia.bianchi@example.org"},
				"subject": "Invoice 2026-118", "attachments": []any{map[string]any{"filename": "invoice-2026-118.pdf",
					"contentType": "application/pdf", "size": 48213, "partId": "2"}}},
		}}},
	}
}

func testRouteResultExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"matched": {Summary: "Matched, with the mapped body and the per-term trace",
			Value: map[string]any{"matched": true, "costUsed": 52, "cost": 52, "durationMs": 1,
				"mapped": map[string]any{"messageId": "msg_01m5ch2yg09swc93g54trswp15",
					"from": "giulia.bianchi@example.org", "subject": "Invoice 2026-118"},
				"trace": []any{
					map[string]any{"expr": `(type == "message.received")`, "result": true},
					map[string]any{"expr": `(domain(data.from) in ["example.org", "example.net"])`, "result": true},
					map[string]any{"expr": `(hasAttachments())`, "result": true},
				}}},
		"noMatch": {Summary: "No match; the trace shows the false term",
			Value: map[string]any{"matched": false, "costUsed": 21, "cost": 21, "durationMs": 0,
				"trace": []any{
					map[string]any{"expr": `(type == "message.received")`, "result": true},
					map[string]any{"expr": `(domain(data.from) in ["example.org", "example.net"])`, "result": false},
				}}},
		"compileError": {Summary: "Draft that does not compile",
			Value: map[string]any{"matched": false, "costUsed": 0, "cost": 0, "durationMs": 0,
				"error":   "ERROR: <input>:1:31: undeclared reference to 'subjctContains'",
				"errorAt": map[string]any{"path": "filter", "line": 1, "column": 31}}},
	}
}

func exampleRouteVersions() map[string]any {
	return map[string]any{"data": []any{
		map[string]any{"id": "rtv_01m5dckp30mdexhj4qp140dym4", "organizationId": exampleOrgID, "routeId": exampleRouteID,
			"version": 3, "filter": exampleCompiled, "map": exampleMap, "builder": exampleBuilder(),
			"createdBy": exampleUserID, "createdAt": "2026-10-26T14:20:05Z"},
		map[string]any{"id": "rtv_01m5d8c6505c3fnkwbkknhkq61", "organizationId": exampleOrgID, "routeId": exampleRouteID,
			"version": 2, "filter": `type == "message.received" && hasAttachments()`,
			"createdBy": "key_01m5cqe6d0rs9cyxvy69ynf0t6", "createdAt": "2026-10-23T11:02:40Z"},
	}, "total": 3}
}

func exampleRouteDryRunRequest() map[string]any {
	return map[string]any{"windowHours": 24, "limit": 1000, "builder": exampleBuilder(), "map": exampleMap}
}

func exampleJobAccepted() map[string]any {
	return map[string]any{"job": map[string]any{
		"id": exampleJobID, "organizationId": exampleOrgID, "kind": JobKindDryRun, "state": "queued",
		"createdBy": exampleUserID, "createdAt": "2026-10-27T10:00:00Z",
	}}
}

func exampleRuleList(withEntries bool) map[string]any {
	out := map[string]any{"id": exampleListID, "organizationId": exampleOrgID, "name": "vip-customers",
		"kind": "addresses", "entryCount": 3, "createdAt": "2026-10-22T10:00:00Z",
		"updatedAt": "2026-10-26T16:45:12Z", "version": 2}
	if withEntries {
		out["entries"] = []any{"giulia.bianchi@example.org", "m.rossi@example.net", "studio.verdi@example.com"}
	}
	return out
}

func exampleRuleListPage() map[string]any {
	return map[string]any{"data": []any{exampleRuleList(false), map[string]any{
		"id": "rls_01m5dq6dy0bpw4pexpdp4cjcn0", "organizationId": exampleOrgID, "name": "partner-domains",
		"kind": "domains", "entryCount": 12, "createdAt": "2026-10-23T08:30:00Z", "updatedAt": "2026-10-23T08:30:00Z",
		"version": 1}}, "nextCursor": nil, "total": 2}
}
