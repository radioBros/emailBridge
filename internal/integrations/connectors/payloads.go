package connectors

// CEL map expressions for chat platforms. Kept as constants so tests can assert exact strings.

// Slack Block Kit–style maps (fallback text + one section block).
const (
	MapSlackNewMail = `{"text":"New mail: " + string(data.subject),` +
		`"blocks":[{"type":"header","text":{"type":"plain_text","text":"New mail"}},` +
		`{"type":"section","text":{"type":"mrkdwn","text":"*" + string(data.subject) + "*\nFrom: " + (has(data.from) && type(data.from) == map ? string(data.from.address) : "")}}]}`
	MapSlackDelivery = `{"text":"Delivery failure: " + string(type),` +
		`"blocks":[{"type":"header","text":{"type":"plain_text","text":"Delivery failure"}},` +
		`{"type":"section","text":{"type":"mrkdwn","text":string(type) + "\n" + string(data.subject)}}]}`
	MapSlackHealth = `{"text":"Account health: " + string(type),` +
		`"blocks":[{"type":"header","text":{"type":"plain_text","text":"Account health"}},` +
		`{"type":"section","text":{"type":"mrkdwn","text":string(type) + " — " + string(data.accountId)}}]}`
	MapSlackQuota = `{"text":"Quota warning: " + string(data.accountId),` +
		`"blocks":[{"type":"header","text":{"type":"plain_text","text":"Quota warning"}},` +
		`{"type":"section","text":{"type":"mrkdwn","text":string(type) + " — " + string(data.reason)}}]}`
)

// Teams Adaptive Card maps.
const (
	MapTeamsNewMail = `{"@type":"MessageCard","@context":"https://schema.org/extensions","summary":"New mail",` +
		`"themeColor":"0078D4","title":"New mail","text":string(data.subject) + " — " + (has(data.from) && type(data.from) == map ? string(data.from.address) : "")}`
	MapTeamsDelivery = `{"@type":"MessageCard","@context":"https://schema.org/extensions","summary":"Delivery failure",` +
		`"themeColor":"D13438","title":"Delivery failure","text":string(type) + ": " + string(data.subject)}`
	MapTeamsHealth = `{"@type":"MessageCard","@context":"https://schema.org/extensions","summary":"Account health",` +
		`"themeColor":"FFB900","title":"Account health","text":string(type) + " — " + string(data.accountId)}`
	MapTeamsQuota = `{"@type":"MessageCard","@context":"https://schema.org/extensions","summary":"Quota warning",` +
		`"themeColor":"FF8C00","title":"Quota warning","text":string(type) + " — " + string(data.reason)}`
)

// Discord embed maps.
const (
	MapDiscordNewMail = `{"content":"New mail","embeds":[{"title":string(data.subject),` +
		`"description":"From: " + (has(data.from) && type(data.from) == map ? string(data.from.address) : ""),"color":5793266}]}`
	MapDiscordDelivery = `{"content":"Delivery failure","embeds":[{"title":string(type),` +
		`"description":string(data.subject),"color":15158332}]}`
	MapDiscordHealth = `{"content":"Account health","embeds":[{"title":string(type),` +
		`"description":string(data.accountId),"color":16776960}]}`
	MapDiscordQuota = `{"content":"Quota warning","embeds":[{"title":"Quota / mailbox",` +
		`"description":string(type) + " — " + string(data.reason),"color":16744192}]}`
)

// Telegram sendMessage body maps (chat_id filled at install via template in endpoint URL path;
// map supplies text only — full body built by route map including chat_id from config).
const (
	MapTelegramNewMail = `{"chat_id":"{{config.chatId}}","text":"New mail: " + string(data.subject) +` +
		`"\nFrom: " + (has(data.from) && type(data.from) == map ? string(data.from.address) : ""),"parse_mode":"HTML"}`
	MapTelegramDelivery = `{"chat_id":"{{config.chatId}}","text":"Delivery failure: " + string(type) +` +
		`"\n" + string(data.subject),"parse_mode":"HTML"}`
	MapTelegramHealth = `{"chat_id":"{{config.chatId}}","text":"Account health: " + string(type) +` +
		`" — " + string(data.accountId),"parse_mode":"HTML"}`
	MapTelegramQuota = `{"chat_id":"{{config.chatId}}","text":"Quota warning: " + string(type) +` +
		`" — " + string(data.reason),"parse_mode":"HTML"}`
)

// Mattermost attachment maps.
const (
	MapMattermostNewMail = `{"text":"New mail","attachments":[{"color":"#4A154B",` +
		`"title":string(data.subject),"text":"From: " + (has(data.from) && type(data.from) == map ? string(data.from.address) : "")}]}`
	MapMattermostDelivery = `{"text":"Delivery failure","attachments":[{"color":"#D13438",` +
		`"title":string(type),"text":string(data.subject)}]}`
	MapMattermostHealth = `{"text":"Account health","attachments":[{"color":"#FFB900",` +
		`"title":string(type),"text":string(data.accountId)}]}`
	MapMattermostQuota = `{"text":"Quota warning","attachments":[{"color":"#FF8C00",` +
		`"title":"Quota / mailbox","text":string(type) + " — " + string(data.reason)}]}`
)

// FeatherDesk create-ticket map (email → ticket).
const MapFeatherDeskTicket = `{"subject":string(data.subject),"requester":(has(data.from) && type(data.from) == map ? string(data.from.address) : ""),` +
	`"body":string(data.text),"mailboxId":"{{config.mailboxId}}","externalId":string(data.messageId)}`

// Generic helpdesk passthrough-ish map.
const MapHelpdeskDefault = `{"event":string(type),"subject":string(data.subject),` +
	`"from":(has(data.from) && type(data.from) == map ? string(data.from.address) : ""),"accountId":string(data.accountId)}`
