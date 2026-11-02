package connectors

// Mattermost returns the Mattermost incoming-webhook connector.
func Mattermost() Def {
	props := chatEventFields()
	props["webhookUrl"] = map[string]any{
		"type": "string", "format": "uri", "title": "Webhook URL",
		"description": "Mattermost incoming webhook URL.",
	}
	return Def{
		ID: IDMattermost, Version: "1.0.0", Name: "Mattermost", Category: CategoryChat,
		Icon: "mdi-mattermost", Color: "#0058CC",
		Description: "Post mail alerts to Mattermost with attachments. " +
			"Subjects and selected event fields leave EmailBridge to Mattermost.",
		DocsPath: "/docs/integrations/mattermost",
		ConfigSchema: map[string]any{
			"type": "object", "required": []any{"webhookUrl"},
			"properties": props,
		},
		Capabilities: []string{CapTest},
		Endpoints: []EndpointTmpl{{
			Name: "Mattermost alerts", URL: "{{config.webhookUrl}}",
		}},
		Routes: chatRoutes(chatMaps{
			NewMail: MapMattermostNewMail, Delivery: MapMattermostDelivery,
			Health: MapMattermostHealth, Quota: MapMattermostQuota,
		}),
		Tester: TesterMattermost,
	}
}
