# Integrations

EmailBridge ships ready connectors. Installing one creates preconfigured webhook endpoints and
routes you can edit. Every connector is off by default (`integrations.<id>`) and can be disabled
without deleting data.

## Catalogue

### Chat

- [Slack](slack.md)
- [Microsoft Teams](teams.md)
- [Discord](discord.md)
- [Telegram](telegram.md)
- [Mattermost](mattermost.md)

### Helpdesk

- [FeatherDesk](featherdesk.md)
- [Generic helpdesk](helpdesk.md)

### Automation packages

Publisher-side apps (triggers + actions) live under `integrations/` in the repo:

- n8n community node — `integrations/n8n/`
- Zapier CLI app — `integrations/zapier/`
- Make app definition — `integrations/make/`
- Activepieces piece — `integrations/activepieces/`

### Sending providers

- [Amazon SES](ses.md)
- [Postmark](postmark.md)
- [Mailgun](mailgun.md)
- [SendGrid](sendgrid.md)
- [Brevo](brevo.md)

### Spam scoring

- [Rspamd](rspamd.md)

### Storage export

- [Google Drive](gdrive.md)
- [OneDrive / SharePoint](onedrive.md)

### CRM

- [HubSpot](hubspot.md)
- [Pipedrive](pipedrive.md)

### SSO presets

- [Authentik](sso-authentik.md)
- [Keycloak](sso-keycloak.md)
- [Microsoft Entra ID](sso-entra.md)
- [Google Workspace](sso-google.md)
- [Okta](sso-okta.md)

### Observability presets

- [Sentry / GlitchTip](sentry.md)
- [Grafana](grafana.md)
- [Loki](loki.md)
- [OpenTelemetry](otel.md)
