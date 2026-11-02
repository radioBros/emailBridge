# Telegram

Sends alerts with the **Bot API** `sendMessage` method.

## Setup

1. Create a bot with BotFather; copy the token (stored encrypted).
2. Note the chat or channel id.
3. Admin → Integrations → Telegram → Install with `chatId` and `botToken`.

## What leaves EmailBridge

Alert text (subject, from, account id, reason) is sent to Telegram’s API over HTTPS.

Switch: `integrations.telegram`. The bot token never appears in API responses (`secretsSet` only).
