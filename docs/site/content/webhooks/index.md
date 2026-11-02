# Webhooks

Deliveries use **Standard Webhooks** signatures by default and **CloudEvents** JSON envelopes. Routing rules:
[Filters & maps](filters.md).

## Request shape

- `POST` to the endpoint URL
- Headers: `webhook-id`, `webhook-timestamp`, `webhook-signature`, plus `eb-event-type`, `eb-delivery-id`,
  `eb-attempt`, `traceparent`
- Body: CloudEvents envelope (or route-mapped JSON)
- No redirects followed; TLS verified

## Security switches

Per-endpoint signature mode, mTLS, and related switches are set on the webhook endpoint (see the [API](../api/index.md)). Keep defaults on for
production.

## Verifying signatures

Verify the signature with one of the [SDKs](../sdks/index.md) or any Standard Webhooks library.
