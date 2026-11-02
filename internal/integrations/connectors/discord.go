package connectors

// Discord returns the Discord incoming-webhook connector.
func Discord() Def {
	props := chatEventFields()
	props["webhookUrl"] = map[string]any{
		"type": "string", "format": "uri", "title": "Webhook URL",
		"description": "Discord channel webhook URL.",
	}
	return Def{
		ID: IDDiscord, Version: "1.0.0", Name: "Discord", Category: CategoryChat,
		Icon: "mdi-discord", Color: "#5865F2",
		Description: "Post mail alerts to Discord as embeds. " +
			"Subjects and selected event fields leave EmailBridge to Discord.",
		DocsPath: "/docs/integrations/discord",
		ConfigSchema: map[string]any{
			"type": "object", "required": []any{"webhookUrl"},
			"properties": props,
		},
		Capabilities: []string{CapTest},
		Endpoints: []EndpointTmpl{{
			Name: "Discord alerts", URL: "{{config.webhookUrl}}",
		}},
		Routes: chatRoutes(chatMaps{
			NewMail: MapDiscordNewMail, Delivery: MapDiscordDelivery,
			Health: MapDiscordHealth, Quota: MapDiscordQuota,
		}),
		Tester: TesterDiscord,
	}
}
