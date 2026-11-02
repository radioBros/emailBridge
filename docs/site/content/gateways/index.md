# Sending gateways

Organisation-level relays for outbound mail. A gateway is either **SMTP** (classic host/port/auth) or an
**API provider** (`ses`, `postmark`, `mailgun`, `sendgrid`, `brevo`).

## SMTP

Host, port, TLS, credentials. Submit dials the gateway instead of the account’s own SMTP
when `accounts.smtp.gatewayId` is set.

## API providers

Create a gateway with the provider kind, non-secret options (region, domain, From, optional `baseUrl`), and
write-only credentials. Secrets are encrypted at rest and never returned by the API.

Paste the **webhook URL** shown in the admin form into the provider’s delivery webhook settings:

`{EB_CALLBACK_URL}/callbacks/gateways/{gatewayId}`

| Provider | Signature |
| --- | --- |
| Mailgun | HMAC of timestamp+token (replay window 15m) |
| SendGrid | ECDSA event webhook (verification key) |
| Postmark | Query `?token=` equals webhook secret |
| Brevo | Bearer token or `?token=` |
| SES | SNS signature + cert host `sns.<region>.amazonaws.com`; SubscriptionConfirmation confirmed via egress |

Kind cannot change after create. SMTP-only fields are refused on API gateways and vice versa.

See also the per-provider pages under [Integrations](/integrations/).
