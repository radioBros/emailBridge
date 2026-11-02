/** UI catalog for Settings tabs (S9 runtime keys → camelCase). */

export type SettingKind =
  | "bool"
  | "int"
  | "float"
  | "duration"
  | "size"
  | "rate"
  | "string"
  | "select"
  | "strings"
  | "text";

export type SettingsTabId =
  | "general"
  | "sync"
  | "sending"
  | "webhooks"
  | "retention"
  | "security"
  | "sysmail"
  | "switches";

export interface SettingFieldDef {
  key: string;
  kind: SettingKind;
  /** global-only keys cannot be edited at organisation scope */
  changeScope?: "global" | "organization" | "both";
  options?: string[];
  unit?: string;
}

export const SETTINGS_TABS: SettingsTabId[] = [
  "general",
  "sync",
  "sending",
  "webhooks",
  "retention",
  "security",
  "sysmail",
  "switches",
];

const F = (
  key: string,
  kind: SettingKind,
  extra?: Partial<SettingFieldDef>,
): SettingFieldDef => ({ key, kind, changeScope: "both", ...extra });

export const SETTINGS_FIELDS: Record<Exclude<SettingsTabId, "switches">, SettingFieldDef[]> = {
  general: [
    F("rateApiKey", "rate"),
    F("rateApiOrg", "rate"),
    F("rateLogin", "rate"),
    F("quotaAccounts", "int"),
    F("quotaSendsPerDay", "int"),
    F("quotaSendsPerAccountMinute", "int"),
    F("quotaEndpoints", "int"),
  ],
  sync: [
    F("syncResyncDelay", "duration"),
    F("syncMaxIdle", "duration"),
    F("syncAuthDisableAfter", "duration"),
    F("syncNoidlePoll", "duration"),
    F("syncFullResync", "duration"),
    F("syncCatchupMax", "duration"),
    F("syncMaxConnectionsPerAccount", "int"),
    F("syncTextMax", "size"),
    F("imapIdName", "string"),
    F("imapIdVersion", "string"),
    F("imapIdVendor", "string"),
    F("imapIdSupportUrl", "string"),
  ],
  sending: [
    F("submitMaxMessageSize", "size"),
    F("submitPolicy", "select", { options: ["standard", "atMostOnce"] }),
    F("submitMaxAttempts", "int"),
    F("submitBackoffBase", "duration"),
    F("submitBackoffJitter", "float"),
    F("sandbox.rewriteTo", "string"),
  ],
  webhooks: [
    F("webhookTimeout", "duration"),
    F("webhookMaxAttempts", "int"),
    F("webhookBackoffBase", "duration"),
    F("webhookBackoffJitter", "float"),
    F("egressBlockLinkLocal", "bool"),
    F("egressBlockPrivate", "bool"),
    F("egressAllow", "strings"),
    F("rulesCostLimit", "int"),
    F("rulesTimeLimit", "duration"),
    F("webhooks.autoDisableAfter", "duration", { changeScope: "organization" }),
  ],
  retention: [
    F("retentionEvents", "duration"),
    F("retentionDeliveries", "duration"),
    F("retentionOutbound", "duration"),
    F("retentionOutboundRaw", "duration"),
    F("retentionSandbox", "duration"),
    F("retentionJobs", "duration"),
    F("retentionAudit", "duration"),
    F("retentionTracking", "duration"),
  ],
  security: [
    F("authSessionTtl", "duration"),
    F("authSessionIdle", "duration"),
    F("authMfaRequired", "bool"),
    F("authLockoutThreshold", "int"),
    F("authLockoutDuration", "duration"),
    F("sentrySampleRate", "float"),
    F("sentryTracesSampleRate", "float"),
    F("sentrySendPii", "bool"),
    F("protocolLogs.accountCap", "size"),
    F("protocolLogs.rawDebugTtl", "duration", { changeScope: "global" }),
    F("authResults.trustedAuthservIds", "strings", { changeScope: "organization" }),
    F("oidc.autoProvision", "bool", { changeScope: "organization" }),
  ],
  sysmail: [
    F("sysmailMode", "select", { options: ["off", "smtp", "account"] }),
    F("sysmailFrom", "string"),
    F("sysmailSmtpHost", "string"),
    F("sysmailSmtpPort", "int"),
    F("sysmailSmtpTls", "select", { options: ["implicit", "starttls", "none"] }),
    F("sysmailSmtpUser", "string"),
    F("sysmailSmtpPass", "string"),
    F("sysmailAccountId", "string"),
  ],
};

export const SOURCE_COLORS: Record<string, string> = {
  default: "grey",
  env: "info",
  global: "primary",
  organization: "success",
  account: "accent",
};

export const valuesEqual = (a: unknown, b: unknown): boolean => {
  if (a === b) return true;
  if (Array.isArray(a) && Array.isArray(b)) {
    return a.length === b.length && a.every((v, i) => valuesEqual(v, b[i]));
  }
  if (a && b && typeof a === "object" && typeof b === "object") {
    return JSON.stringify(a) === JSON.stringify(b);
  }
  return String(a ?? "") === String(b ?? "");
};
