package connectors

// Helpdesk returns the generic helpdesk recipe connector.
func Helpdesk() Def {
	return Def{
		ID: IDHelpdesk, Version: "1.0.0", Name: "Generic helpdesk", Category: CategoryHelpdesk,
		Icon: "mdi-face-agent", Color: "#64748B",
		Description: "Forward selected events to any helpdesk webhook. " +
			"Customise the CEL filter and JSON map after install. Event payloads leave EmailBridge.",
		DocsPath: "/docs/integrations/helpdesk",
		ConfigSchema: map[string]any{
			"type":     "object",
			"required": []any{"webhookUrl"},
			"properties": map[string]any{
				"webhookUrl": map[string]any{
					"type": "string", "format": "uri", "title": "Webhook URL",
				},
				"filter": map[string]any{
					"type": "string", "title": "Event filter CEL",
					"default": `type == "message.received"`,
				},
				"map": map[string]any{
					"type": "string", "title": "Payload map CEL",
					"default":     MapHelpdeskDefault,
					"description": "CEL expression producing the JSON body.",
				},
			},
		},
		Capabilities: []string{CapTest},
		Endpoints: []EndpointTmpl{{
			Name: "Helpdesk webhook", URL: "{{config.webhookUrl}}",
		}},
		Routes: []RouteTmpl{{
			Name: "Helpdesk forward", EndpointIndex: 0,
			Filter: `{{config.filter}}`,
			Map:    `{{config.map}}`,
		}},
		Tester: TesterHelpdesk,
	}
}
