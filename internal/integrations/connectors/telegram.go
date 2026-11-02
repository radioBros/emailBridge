package connectors

// Telegram returns the Telegram Bot API connector.
func Telegram() Def {
	props := chatEventFields()
	props["chatId"] = map[string]any{
		"type": "string", "title": "Chat ID",
		"description": "Telegram chat or channel id that receives alerts.",
	}
	props["botToken"] = map[string]any{
		"type": "string", "writeOnly": true, "title": "Bot token",
		"description": "BotFather token (stored encrypted).",
	}
	return Def{
		ID: IDTelegram, Version: "1.0.0", Name: "Telegram", Category: CategoryChat,
		Icon: "mdi-telegram", Color: "#26A5E4",
		Description: "Post mail alerts via the Telegram Bot API (sendMessage). " +
			"Subjects and selected event fields leave EmailBridge to Telegram.",
		DocsPath: "/docs/integrations/telegram",
		ConfigSchema: map[string]any{
			"type": "object", "required": []any{"chatId", "botToken"},
			"properties": props,
		},
		SecretFields: []string{"botToken"},
		Capabilities: []string{CapTest},
		Endpoints: []EndpointTmpl{{
			Name: "Telegram sendMessage",
			URL:  "https://api.telegram.org/bot{{secrets.botToken}}/sendMessage",
		}},
		Routes: chatRoutes(chatMaps{
			NewMail: MapTelegramNewMail, Delivery: MapTelegramDelivery,
			Health: MapTelegramHealth, Quota: MapTelegramQuota,
		}),
		Tester: TesterTelegram,
	}
}
