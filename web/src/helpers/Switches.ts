/**
 * Feature-switch display helpers — labels/descriptions from lang/<code>/switches.json.
 * Catalog names mirror internal/features/catalog.go (keep in sync; covered by tests).
 */

export const SWITCH_CATALOG_NAMES = [
  "sandbox.enabled",
  "sandbox.recipientPolicy",
  "inbound.authResults",
  "sync.qresync",
  "inbound.archiveRaw",
  "protocolLogs.enabled",
  "protocolLogs.rawDebug",
  "webhook.signature.mode",
  "webhook.secret.perEndpoint",
  "webhook.secret.rotationOverlap",
  "webhook.mtls",
  "webhook.egress.blockPrivate",
  "events.store",
  "events.sse",
  "rules.enabled",
  "auth.password",
  "auth.passkeys",
  "auth.totp",
  "auth.emailOtp",
  "auth.oidc",
  "apikeys.accountBound",
  "configAsCode.enabled",
  "mailbox.webSafeHtml",
  "mailbox.calendarEvents",
  "inbound.replyText",
  "attachments.s3",
  "attachments.antivirus",
  "sending.templates",
  "tracking.opens",
  "tracking.clicks",
  "sending.listUnsubscribe",
  "sending.suppression",
  "sending.throttle",
  "sending.sendWindows",
  "sending.preSendHook",
  "inbound.bounceDetection",
  "oauth.tokenExport",
  "accounts.authServer",
  "accounts.ignoreCertErrors",
  "connect.hostedPage",
  "protocol.msa",
  "protocol.imapProxy",
  "export.enabled",
  "ai.enabled",
  "mcp.enabled",
  "integrations.<connector>",
  "integrations.slack",
  "integrations.zapier",
  "integrations.rspamd",
  "integrations.teams",
  "integrations.discord",
  "integrations.telegram",
  "integrations.mattermost",
  "integrations.featherdesk",
  "integrations.helpdesk",
  "integrations.ses",
  "integrations.postmark",
  "integrations.mailgun",
  "integrations.sendgrid",
  "integrations.brevo",
  "integrations.gdrive",
  "integrations.onedrive",
  "integrations.hubspot",
  "integrations.pipedrive",
  "integrations.sso-authentik",
  "integrations.sso-keycloak",
  "integrations.sso-entra",
  "integrations.sso-google",
  "integrations.sso-okta",
  "integrations.sentry",
  "integrations.grafana",
  "integrations.loki",
  "integrations.otel",
  "pec.enabled",
  "pec.evidenceArchive",
  "notifications.email",
  "search.fullText",
  "ai.embeddings",
  "sinks.nats",
  "sinks.kafka",
  "sinks.sqs",
  "sinks.sns",
  "sinks.pubsub",
] as const;

export type SwitchCatalogName = (typeof SWITCH_CATALOG_NAMES)[number];

type Translate = (key: string) => string;
type Exists = (key: string) => boolean;

/** "protocolLogs.enabled" → "Protocol logs enabled" (never the raw dotted key). */
export const humaniseSwitchName = (name: string): string => {
  const words = name
    .split(".")
    .flatMap((part) =>
      part
        .replace(/[<>]/g, "")
        .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
        .replace(/[-_]/g, " ")
        .split(/\s+/),
    )
    .filter(Boolean)
    .map((w) => w.toLowerCase());
  if (!words.length) return name;
  const joined = words.join(" ");
  return joined.charAt(0).toUpperCase() + joined.slice(1);
};

export const switchLabelOf = (name: string, t: Translate, te: Exists): string => {
  const key = `switches.${name}.label`;
  return te(key) ? t(key) : humaniseSwitchName(name);
};

export const switchDescriptionOf = (
  name: string,
  t: Translate,
  te: Exists,
  backendDescription?: string | null,
): string => {
  const key = `switches.${name}.description`;
  if (te(key)) return t(key);
  const fallback = backendDescription?.trim();
  return fallback || "";
};
