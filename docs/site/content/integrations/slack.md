# Slack

Posts EmailBridge alerts to a Slack channel via an **Incoming Webhook** using Block Kit payloads.

## Setup

1. Create an Incoming Webhook in Slack and copy the URL.
2. Admin → Integrations → Slack → Install. Paste the webhook URL.
3. Optionally override the CEL filters for new mail, delivery failures, account health, and quota warnings.

## What leaves EmailBridge

Event type, message subject, from address, account id, and related fields included in the route `map`
are sent to Slack. Do not enable for mailboxes that must stay inside your network boundary.

## Alerts

| Route | Default events |
| --- | --- |
| New mail | `message.received` |
| Delivery failures | `message.failed`, `message.bounced`, `message.delivery_attempt_failed` |
| Account health | `account.auth_failed`, `account.state_changed`, `account.connect_failed` |
| Quota warnings | `account.connect_failed`, `account.state_changed` |

Disable the instance or turn off `integrations.slack` to stop deliveries without deleting history.
