package integrations

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples of the integration operations (W70). Documentation only: the lint validates every example
// against the generated schema.

const (
	exampleOrgID       = "org_01m5caqpk0kast63yc13wpy2d9"
	exampleInstanceID  = "int_01m5dzndt02er7qagrrs531p8g"
	exampleInstance2ID = "int_01m5e1s5s0fzzsp861d0hb22qd"
)

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func exampleSlackView() map[string]any {
	return map[string]any{
		"id": exampleInstanceID, "organizationId": exampleOrgID, "type": "slack", "definitionVersion": "1.1.0",
		"name": "Slack: #support-mail",
		"config": map[string]any{"webhookUrl": "https://hooks.slack.com/services/T0000000/B0000000/XXXXXXXXXXXXXXXXXXXXXXXX",
			"channelHint": "#support-mail", "digestMinutes": 0},
		"secretsSet": map[string]any{},
		"createdResources": map[string]any{"endpoints": []any{"whe_01m5dgv610p98z84d29j5x3pmt"},
			"routes": []any{"rte_01m5dckp30mdexhj4qp140dym4", "rte_01m5dn2nz0q7h3ncd1j49hxtnb"}},
		"enabled": true, "createdAt": "2026-10-24T09:00:00Z", "updatedAt": "2026-10-26T11:30:00Z", "version": 2,
	}
}

func exampleRspamdView() map[string]any {
	return map[string]any{
		"id": exampleInstance2ID, "organizationId": exampleOrgID, "type": "rspamd", "definitionVersion": "1.0.0",
		"name": "Rspamd scoring", "config": map[string]any{"baseUrl": "http://rspamd.internal.example.com:11333", "timeout": "5s"},
		"secretsSet":       map[string]any{"password": true},
		"createdResources": map[string]any{"settings": []any{"integrations.rspamd.baseUrl", "integrations.rspamd.timeout"}},
		"enabled":          true, "createdAt": "2026-10-25T15:20:00Z", "updatedAt": "2026-10-25T15:20:00Z", "version": 1,
	}
}

func exampleInstanceList() map[string]any {
	return map[string]any{"data": []any{exampleSlackView(), exampleRspamdView()},
		"nextCursor": "eyJpZCI6ImludF8wMW01ZTFzNXMwZnp6c3A4NjFkMGhiMjJxZCJ9"}
}

func exampleCatalogue() map[string]any {
	return map[string]any{"data": []any{
		map[string]any{"id": "slack", "version": "1.1.0", "name": "Slack", "category": "chat", "icon": "mdi-slack",
			"color": "#4A154B", "description": "Post mail alerts to Slack (Block Kit): new mail, delivery failures, " +
				"account health, and quota warnings. Subjects and selected event fields leave EmailBridge.",
			"docsPath": "/docs/integrations/slack",
			"configSchema": map[string]any{"type": "object", "required": []any{"webhookUrl"}, "properties": map[string]any{
				"webhookUrl":  map[string]any{"type": "string", "format": "uri", "title": "Webhook URL"},
				"channelHint": map[string]any{"type": "string", "title": "Channel hint", "maxLength": 80},
			}},
			"secretFields": []any{}, "capabilities": []any{"test"}, "installedCount": 1,
			"instanceIds": []any{exampleInstanceID}},
		map[string]any{"id": "rspamd", "version": "1.0.0", "name": "Rspamd", "category": "spam", "icon": "mdi-shield-bug", "color": "#E74C3C",
			"description": "Score inbound mail with Rspamd. When sync scoring is enabled, raw message bytes " +
				"are sent to your Rspamd instance; results appear on message.received.spam.",
			"docsPath": "/docs/integrations/rspamd",
			"configSchema": map[string]any{"type": "object", "required": []any{"baseUrl"}, "properties": map[string]any{
				"baseUrl":  map[string]any{"type": "string", "format": "uri", "title": "Rspamd base URL"},
				"timeout":  map[string]any{"type": "string", "title": "Timeout", "default": "5s"},
				"password": map[string]any{"type": "string", "writeOnly": true, "title": "Controller password"},
			}},
			"secretFields": []any{"password"}, "capabilities": []any{"test"}, "installedCount": 0},
	}}
}
