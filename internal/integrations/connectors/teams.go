package connectors

// Teams returns the Microsoft Teams incoming-webhook connector.
func Teams() Def {
	props := chatEventFields()
	props["webhookUrl"] = map[string]any{
		"type": "string", "format": "uri", "title": "Webhook URL",
		"description": "Teams Incoming Webhook URL for the target channel.",
	}
	return Def{
		ID: IDTeams, Version: "1.0.0", Name: "Microsoft Teams", Category: CategoryChat,
		Icon: "mdi-microsoft-teams", Color: "#6264A7",
		Description: "Post mail alerts to Microsoft Teams as Adaptive Cards. " +
			"Subjects and selected event fields leave EmailBridge to Teams.",
		DocsPath: "/docs/integrations/teams",
		ConfigSchema: map[string]any{
			"type": "object", "required": []any{"webhookUrl"},
			"properties": props,
		},
		Capabilities: []string{CapTest},
		Endpoints: []EndpointTmpl{{
			Name: "Teams alerts", URL: "{{config.webhookUrl}}",
		}},
		Routes: chatRoutes(chatMaps{
			NewMail: MapTeamsNewMail, Delivery: MapTeamsDelivery,
			Health: MapTeamsHealth, Quota: MapTeamsQuota,
		}),
		Tester: TesterTeams,
	}
}
