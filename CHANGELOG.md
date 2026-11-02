# Changelog

All notable changes to EmailBridge are listed here. Versions follow [Semantic Versioning](https://semver.org).

## 1.0.0 — 2026-11-02

First public release.

- Mailbox accounts over IMAP/SMTP, Gmail API and Microsoft Graph, with OAuth, IDLE/push sync, folders,
  threads, full-text and semantic search, attachments, exports and archive.
- Italian certified mail (PEC): sending, receipts, signature checks.
- Durable send queue: retries with error classification, rate limits, suppression lists, bounce handling,
  open/click tracking, Handlebars and MJML templates, copy to Sent.
- Webhooks: Standard Webhooks signatures, CloudEvents payloads, CEL filter and map rules with a visual
  editor and presets, ordering, retries, replay, dead-lettering; NATS, Kafka, SQS, SNS and Pub/Sub sinks.
- Routing rules, named lists and per-account metadata.
- Admin UI (Vue 3 + Vuetify) in 9 languages: en, it, es, fr, de, pt, ru, zh, ja.
- Multi-tenant organisations, members and roles; API keys with per-resource scopes; OIDC SSO, passkeys,
  TOTP and email codes; audit log.
- Hosted connect page with branding (logos, palette, header HTML).
- Integrations catalogue, MCP server, optional AI providers (OpenAI, Anthropic, Gemini, xAI).
- Cluster mode on NATS JetStream with leases and role split; Prometheus metrics, OpenTelemetry tracing,
  Sentry-compatible error reporting; GDPR export and erasure; config as code.
- Deployment: Compose (small, medium), Podman Quadlet, Helm chart, AWS ECS Terraform.
- SDKs for Go, TypeScript, Python and PHP; a provider simulator for testing.
