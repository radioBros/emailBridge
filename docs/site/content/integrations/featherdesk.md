# FeatherDesk

Two-way helpdesk bridge:

- **Email → ticket:** `message.received` (default) creates a ticket via FeatherDesk
  `POST /api/v1/tickets`.
- **Ticket reply → mail:** FeatherDesk calls
  `POST /integrations/{instanceId}/callback` with HMAC
  `X-FeatherDesk-Signature: sha256=<hex>`; EmailBridge sends a threaded reply.

## Setup

1. Note your FeatherDesk base URL and API key.
2. Admin → Integrations → FeatherDesk → Install (`baseUrl`, `apiKey`, optional `mailboxId`,
   `callbackToken`).
3. Configure FeatherDesk to POST replies to
   `POST /integrations/{instanceId}/callback` with headers
   `X-FeatherDesk-Timestamp` (Unix seconds) and
   `X-FeatherDesk-Signature: sha256=<HMAC of "<ts>."+body>` using the callback token.
   Signatures older than 5 minutes are rejected.

## What leaves EmailBridge

Subject, requester address, body text and message id are sent to FeatherDesk. Replies send mail
through your connected account.

Switch: `integrations.featherdesk`. Disable stops both directions’ owned routes/endpoints.
