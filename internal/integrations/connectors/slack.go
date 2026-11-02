package connectors

// Slack returns the Slack incoming-webhook connector (P8.2, replaces P8.1 1.0.0).
func Slack() Def {
	props := chatEventFields()
	props["webhookUrl"] = map[string]any{
		"type": "string", "format": "uri", "title": "Webhook URL",
		"description": "Slack Incoming Webhook URL (https://hooks.slack.com/...)",
	}
	props["channelHint"] = map[string]any{
		"type": "string", "title": "Channel hint", "maxLength": 80,
		"description": "Optional label for the admin UI (Slack channel is fixed by the webhook).",
	}
	return Def{
		ID: IDSlack, Version: "1.1.0", Name: "Slack", Category: CategoryChat,
		Icon: "mdi-slack", Color: "#4A154B",
		Description: "Post mail alerts to Slack (Block Kit): new mail, delivery failures, " +
			"account health, and quota warnings. Subjects and selected event fields leave EmailBridge.",
		DocsPath: "/docs/integrations/slack",
		ConfigSchema: map[string]any{
			"type": "object", "required": []any{"webhookUrl"},
			"properties": props,
		},
		Capabilities: []string{CapTest},
		Endpoints: []EndpointTmpl{{
			Name: "Slack alerts", URL: "{{config.webhookUrl}}",
		}},
		Routes: chatRoutes(chatMaps{
			NewMail: MapSlackNewMail, Delivery: MapSlackDelivery,
			Health: MapSlackHealth, Quota: MapSlackQuota,
		}),
		Tester: TesterSlack,
	}
}
