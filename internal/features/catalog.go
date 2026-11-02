package features

// DefaultRegistry returns a registry with every switch from S9 §4.2.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	if err := r.Register(catalogue...); err != nil {
		// Catalogue is static; a duplicate here is a programming error.
		panic(err)
	}
	return r
}

var on = BoolValue(true)
var off = BoolValue(false)

// catalogue is every switch in the S9 §4.2 matrix.
var catalogue = []Switch{
	{
		Name: "sandbox.enabled", Description: "Capture outgoing mail instead of sending",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount}, Phase: "P1", Lockable: true,
		Default: off,
		DefaultFor: func(env string) Value {
			if env != "production" {
				return BoolValue(true)
			}
			return BoolValue(false)
		},
	},
	{
		Name: "sandbox.recipientPolicy", Description: "allow, rewrite, drop, or capture recipients",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Phase: "P1", Lockable: true,
		Default: StringValue("allow"),
		DefaultFor: func(env string) Value {
			if env == "production" {
				return StringValue("allow")
			}
			return StringValue("capture")
		},
	},
	{
		Name: "inbound.authResults", Description: "SPF/DKIM/DMARC/ARC results in message.received",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount}, Default: off, Phase: "P1",
	},
	{
		// QRESYNC change detection (RFC 7162) through the patched go-imap (third_party/go-imap); off until
		// it has run in production on the CONDSTORE-verified P1 path (S5 §4).
		Name: "sync.qresync", Description: "QRESYNC change detection when the server offers it",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount}, Default: off, Phase: "P2",
	},
	{
		Name: "inbound.archiveRaw", Description: "Store raw source of every new message in S3",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P1",
	},
	{
		Name: "protocolLogs.enabled", Description: "Per-account IMAP/SMTP transcripts",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P1", Lockable: true,
	},
	{
		Name: "protocolLogs.rawDebug", Description: "Full protocol bytes (auto-off after TTL)",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P1", Lockable: true,
	},
	{
		Name: "webhook.signature.mode", Description: "standard or bodyOnly signature mode",
		Scopes:  []Scope{ScopeGlobal, ScopeOrganization, ScopeEndpoint},
		Default: StringValue("standard"), Phase: "P1",
	},
	{
		Name: "webhook.secret.perEndpoint", Description: "Per-endpoint webhook secrets",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P1",
	},
	{
		Name: "webhook.secret.rotationOverlap", Description: "Dual-sign during secret rotation",
		Scopes: []Scope{ScopeEndpoint}, Default: on, Phase: "P1",
	},
	{
		Name: "webhook.mtls", Description: "Client certificate on webhook delivery",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: off, Phase: "P3",
	},
	{
		Name: "webhook.egress.blockPrivate", Description: "Block RFC1918/ULA/loopback egress",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: off, Phase: "P1", Lockable: true,
		// Default follows EB_EGRESS_BLOCK_PRIVATE (false); runtime wiring overlays the env value.
	},
	{
		Name: "events.store", Description: "Event store kept for replay",
		Scopes: []Scope{ScopeGlobal}, Default: on, Phase: "P1",
	},
	{
		Name: "events.sse", Description: "/v1/events/stream",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P3",
	},
	{
		Name: "rules.enabled", Description: "Routes evaluated",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P1",
	},
	sink("sinks.nats"), sink("sinks.kafka"), sink("sinks.sqs"), sink("sinks.sns"), sink("sinks.pubsub"),
	{
		Name: "auth.password", Description: "Password authentication",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P0",
	},
	{
		Name: "auth.passkeys", Description: "Passkey authentication",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P0",
	},
	{
		Name: "auth.totp", Description: "TOTP MFA",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P0",
	},
	{
		Name: "auth.emailOtp", Description: "Email OTP MFA (needs system mail)",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P0",
	},
	{
		Name: "auth.oidc", Description: "OIDC SSO for the organisation",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P3", Lockable: true,
	},
	{
		Name: "apikeys.accountBound", Description: "Allow API keys bound to one account",
		Scopes: []Scope{ScopeOrganization}, Default: on, Phase: "P0",
	},
	{
		Name: "configAsCode.enabled", Description: "YAML plan/apply via CLI and API",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: off, Phase: "P3", Lockable: true,
	},
	{
		Name: "mailbox.webSafeHtml", Description: "Allow sanitised HTML in payloads (endpoint payload opts in)",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: on, Phase: "P4",
	},
	{
		Name: "mailbox.calendarEvents", Description: "Allow parsed iCalendar in payloads (endpoint payload opts in)",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: on, Phase: "P4",
	},
	{
		Name: "inbound.replyText", Description: "Allow reply text extraction (endpoint payload opts in)",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: on, Phase: "P4",
	},
	{
		Name: "attachments.s3", Description: "Attachments to S3 + signed URLs",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P4",
	},
	{
		Name: "attachments.antivirus", Description: "ClamAV scan before delivery",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount}, Default: off, Phase: "P4", Lockable: true,
	},
	{
		Name: "sending.templates", Description: "Send templates",
		Scopes: []Scope{ScopeOrganization}, Default: on, Phase: "P5",
	},
	{
		Name: "tracking.opens", Description: "Open tracking",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P5", Lockable: true,
	},
	{
		Name: "tracking.clicks", Description: "Click tracking",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P5", Lockable: true,
	},
	{
		Name: "sending.listUnsubscribe", Description: "RFC 8058 List-Unsubscribe",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P5",
	},
	{
		Name: "sending.suppression", Description: "Suppression lists checked before send",
		Scopes: []Scope{ScopeOrganization}, Default: on, Phase: "P5",
	},
	{
		Name: "sending.throttle", Description: "Per-provider/per-domain throttling",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount}, Default: on, Phase: "P5",
	},
	{
		Name: "sending.sendWindows", Description: "Time-zone send windows",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P5",
	},
	{
		Name: "sending.preSendHook", Description: "Policy hook before send",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P5",
	},
	{
		Name: "inbound.bounceDetection", Description: "Bounce/ARF/auto-reply classification",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: on, Phase: "P5",
	},
	{
		Name: "oauth.tokenExport", Description: "GET …/oauth-token",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P6", Lockable: true,
	},
	{
		Name: "accounts.authServer", Description: "External credential server",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P6", Lockable: true,
	},
	{
		Name: "accounts.ignoreCertErrors", Description: "Accept invalid mail-server certificates",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P1", Lockable: true,
	},
	{
		Name: "connect.hostedPage", Description: "Hosted connect page",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P6",
	},
	{
		Name: "protocol.msa", Description: "SMTP submission server",
		Scopes: []Scope{ScopeGlobal}, Default: off, Phase: "P7",
	},
	{
		Name: "protocol.imapProxy", Description: "IMAP proxy",
		Scopes: []Scope{ScopeGlobal}, Default: off, Phase: "P7",
	},
	{
		Name: "export.enabled", Description: "Bulk export",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P7", Lockable: true,
	},
	{
		Name: "ai.enabled", Description: "AI plugin",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount}, Default: off, Phase: "P7", Lockable: true,
	},
	{
		Name: "mcp.enabled", Description: "MCP endpoint",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: off, Phase: "P7", Lockable: true,
	},
	{
		// Template entry for per-connector switches; concrete names also registered below / at module start.
		Name: "integrations.<connector>", Description: "Connector integration (one per connector)",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.slack", Description: "Slack incoming webhook connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.zapier", Description: "Zapier/Make webhook connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.rspamd", Description: "Rspamd spam scoring connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.teams", Description: "Microsoft Teams webhook connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.discord", Description: "Discord webhook connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.telegram", Description: "Telegram Bot API connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.mattermost", Description: "Mattermost webhook connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.featherdesk", Description: "FeatherDesk helpdesk connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	{
		Name: "integrations.helpdesk", Description: "Generic helpdesk webhook connector",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
	},
	// P8.3 connectors (also registered dynamically at module start)
	{Name: "integrations.ses", Description: "Amazon SES sending connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.postmark", Description: "Postmark sending connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.mailgun", Description: "Mailgun sending connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sendgrid", Description: "SendGrid sending connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.brevo", Description: "Brevo sending connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.gdrive", Description: "Google Drive storage connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.onedrive", Description: "OneDrive/SharePoint storage connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.hubspot", Description: "HubSpot CRM connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.pipedrive", Description: "Pipedrive CRM connector", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sso-authentik", Description: "Authentik SSO preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sso-keycloak", Description: "Keycloak SSO preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sso-entra", Description: "Microsoft Entra ID SSO preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sso-google", Description: "Google Workspace SSO preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sso-okta", Description: "Okta SSO preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.sentry", Description: "Sentry/GlitchTip observability preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.grafana", Description: "Grafana dashboard preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.loki", Description: "Loki labels preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{Name: "integrations.otel", Description: "OpenTelemetry collector preset", Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P8", Lockable: true},
	{
		Name: "pec.enabled", Description: "PEC module for the account",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P9",
	},
	{
		Name: "pec.evidenceArchive", Description: "Archive PEC receipts/envelopes with Object Lock",
		Scopes: []Scope{ScopeOrganization}, Default: off, Phase: "P9",
	},
	{
		Name: "notifications.email", Description: "Email channel for system notifications (sysmail alert)",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: on, Phase: "P7", Lockable: true,
	},
	{
		// G3: the organisation value is the master switch; an account can opt out (internal/fulltext).
		Name: "search.fullText", Description: "Full-text search across the organisation's accounts",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P10", Lockable: true,
	},
	{
		// G1: same rule as search.fullText (organisation master, account opt-out; internal/embeddings).
		Name: "ai.embeddings", Description: "Semantic search and questions over a mailbox (embeddings)",
		Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off, Phase: "P10", Lockable: true,
	},
}

func sink(name string) Switch {
	return Switch{
		Name: name, Description: "Sink type available",
		Scopes: []Scope{ScopeGlobal, ScopeOrganization}, Default: off, Phase: "P3", Lockable: true,
	}
}
