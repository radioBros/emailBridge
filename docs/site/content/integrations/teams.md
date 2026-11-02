# Microsoft Teams

Posts alerts to a Teams channel via an **Incoming Webhook** as Adaptive Card–style `MessageCard` JSON.

## Setup

1. Add an Incoming Webhook connector to the Teams channel.
2. Admin → Integrations → Microsoft Teams → Install with the webhook URL.
3. Tune CEL filters per alert kind if needed.

## What leaves EmailBridge

Subjects, account identifiers and failure reasons included in the payload map are sent to Microsoft.

Disable the instance or `integrations.teams` to stop alerts with no other side effects.
