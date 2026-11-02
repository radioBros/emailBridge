import type { ValidationRule } from "@/constants/Validations";
import { combine, email, required } from "@/constants/Validations";
import type {
  AccountView,
  AccountsPatchBody,
  AccountsSyncConfig,
  CreateInput,
  IMAPConfigInput,
  SMTPConfigInput,
  VerifyRequest,
} from "@/api/accounts";
import type { SendPolicy, TlsMode } from "./accountState";
import { isLocale } from "@/app/locales";
import {
  metadataFromRows,
  metadataMergePatch,
  rowsFromMetadata,
  type AccountMetadata,
  type MetadataRow,
} from "./accountMetadata";

/** Local until schema regen (G4): the account view with its free-form metadata. */
export type AccountViewG4 = AccountView & { metadata?: AccountMetadata | null };

/** True when the account locale is one of the nine product codes. */
export const isAccountLocale = (v: unknown): v is string => isLocale(v);

/** Local form model (passwords write-only; empty on edit means unchanged). */
export interface AccountFormModel {
  name: string;
  email: string;
  externalId: string;
  tz: string;
  locale: string;
  imapHost: string;
  imapPort: number | null;
  imapTls: TlsMode;
  imapUser: string;
  imapPass: string;
  imapIdName: string;
  imapIdVersion: string;
  imapIdVendor: string;
  imapIdSupportUrl: string;
  imapDisableRev2: boolean;
  netProxy: string;
  netLocalAddress: string;
  netEhloName: string;
  netLocalAddresses: string;
  netSmtpStrategy: string;
  netImapStrategy: string;
  smtpHost: string;
  smtpPort: number | null;
  smtpTls: TlsMode;
  smtpUser: string;
  smtpPass: string;
  smtpPolicy: SendPolicy;
  smtpMaxAttempts: number | null;
  smtpBackoffBase: string;
  syncPaths: string;
  idlePaths: string;
  notifyFrom: string;
  syncFrom: string;
  resyncDelay: string;
  maxIdle: string;
  ignoreCertErrors: boolean;
  /** Account switch `pec.enabled` (S17 §10). Sent only when on, so an organisation-wide default still applies. */
  pecEnabled: boolean;
  /** Free-form metadata rows (G4). */
  metadata: MetadataRow[];
}

const browserTz = (): string => {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
};

export const emptyForm = (): AccountFormModel => ({
  name: "",
  email: "",
  externalId: "",
  tz: browserTz(),
  locale: "it",
  imapHost: "",
  imapPort: 993,
  imapTls: "implicit",
  imapUser: "",
  imapPass: "",
  imapIdName: "",
  imapIdVersion: "",
  imapIdVendor: "",
  imapIdSupportUrl: "",
  imapDisableRev2: false,
  netProxy: "",
  netLocalAddress: "",
  netEhloName: "",
  netLocalAddresses: "",
  netSmtpStrategy: "",
  netImapStrategy: "",
  smtpHost: "",
  smtpPort: 465,
  smtpTls: "implicit",
  smtpUser: "",
  smtpPass: "",
  smtpPolicy: "standard",
  smtpMaxAttempts: null,
  smtpBackoffBase: "",
  syncPaths: "*",
  idlePaths: "INBOX",
  notifyFrom: "",
  syncFrom: "",
  resyncDelay: "",
  maxIdle: "",
  ignoreCertErrors: false,
  pecEnabled: false,
  metadata: [],
});

export const formFromAccount = (a: AccountView): AccountFormModel => ({
  name: a.name,
  email: a.email,
  externalId: a.externalId ?? "",
  tz: a.tz || browserTz(),
  locale: isAccountLocale(a.locale) ? a.locale : "it",
  imapHost: a.imap.host,
  imapPort: a.imap.port,
  imapTls: (a.imap.tls as TlsMode) || "implicit",
  imapUser: a.imap.auth.user,
  imapPass: "",
  imapIdName: a.imap.idName ?? "",
  imapIdVersion: (a.imap as { idVersion?: string }).idVersion ?? "",
  imapIdVendor: (a.imap as { idVendor?: string }).idVendor ?? "",
  imapIdSupportUrl: (a.imap as { idSupportUrl?: string }).idSupportUrl ?? "",
  imapDisableRev2: (a.imap as { disableRev2?: boolean }).disableRev2 === true,
  netProxy: a.network?.proxy ?? "",
  netLocalAddress: a.network?.localAddress ?? "",
  netEhloName: a.network?.ehloName ?? "",
  netLocalAddresses: ((a.network as { localAddresses?: string[] } | undefined)?.localAddresses ?? []).join(", "),
  netSmtpStrategy: (a.network as { smtpStrategy?: string } | undefined)?.smtpStrategy ?? "",
  netImapStrategy: (a.network as { imapStrategy?: string } | undefined)?.imapStrategy ?? "",
  smtpHost: a.smtp.host,
  smtpPort: a.smtp.port,
  smtpTls: (a.smtp.tls as TlsMode) || "implicit",
  smtpUser: a.smtp.auth.user,
  smtpPass: "",
  smtpPolicy: (a.smtp.policy as SendPolicy) || "standard",
  smtpMaxAttempts: a.smtp.maxAttempts ?? null,
  smtpBackoffBase: a.smtp.backoffBase ?? "",
  syncPaths: (a.sync.paths ?? ["*"]).join(", "),
  idlePaths: (a.sync.idlePaths ?? ["INBOX"]).join(", "),
  notifyFrom: a.sync.notifyFrom?.slice(0, 16) ?? "",
  syncFrom: a.sync.syncFrom?.slice(0, 16) ?? "",
  resyncDelay: a.sync.resyncDelay ?? "",
  maxIdle: a.sync.maxIdle ?? "",
  ignoreCertErrors: a.switches?.["accounts.ignoreCertErrors"] === true,
  pecEnabled: a.switches?.["pec.enabled"] === true,
  metadata: rowsFromMetadata((a as AccountViewG4).metadata),
});

/** Hostname / IP shape (empty values pass — compose with `required`). */
export const hostRules: ValidationRule[] = [
  (value): true | string => {
    const s = value == null ? "" : String(value).trim();
    if (!s) return true;
    if (s.length > 253) return "invalid_host";
    const ok =
      /^(?:(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}|\d{1,3}(?:\.\d{1,3}){3}|localhost|\[[0-9a-fA-F:]+\])$/.test(
        s,
      );
    return ok ? true : "invalid_host";
  },
];

/** Port 1–65535 (empty passes — compose with `required`). */
export const portRules: ValidationRule[] = [
  (value): true | string => {
    if (value === null || value === undefined || value === "") return true;
    const n = typeof value === "number" ? value : Number(String(value).trim());
    if (!Number.isInteger(n) || n < 1 || n > 65535) return "invalid_port";
    return true;
  },
];

export const identityRules = {
  name: required,
  email: combine(required, email),
};

export const imapRules = {
  host: combine(required, hostRules),
  port: combine(required, portRules),
  user: required,
};

export const smtpRules = {
  host: combine(required, hostRules),
  port: combine(required, portRules),
  user: required,
};

const splitPaths = (raw: string): string[] =>
  raw
    .split(/[,;\n]/)
    .map((p) => p.trim())
    .filter(Boolean);

const toIsoOrUndef = (local: string): string | undefined => {
  const s = local.trim();
  if (!s) return undefined;
  // datetime-local → ISO-ish; server accepts date-time
  return s.length === 16 ? `${s}:00Z` : s;
};

/** Local until schema regen (W57). */
type ImapW57 = IMAPConfigInput & {
  idVersion?: string;
  idVendor?: string;
  idSupportUrl?: string;
  disableRev2?: boolean;
};

/** Local until schema regen (W57). */
type NetworkW57 = {
  proxy?: string;
  localAddress?: string;
  ehloName?: string;
  localAddresses?: string[];
  smtpStrategy?: string;
  imapStrategy?: string;
};

const buildImap = (f: AccountFormModel, includePass: boolean): ImapW57 => {
  const auth: IMAPConfigInput["auth"] = { user: f.imapUser.trim() };
  if (includePass && f.imapPass) auth.pass = f.imapPass;
  const cfg: ImapW57 = {
    host: f.imapHost.trim(),
    port: Number(f.imapPort),
    tls: f.imapTls,
    auth,
  };
  if (f.imapIdName.trim()) cfg.idName = f.imapIdName.trim();
  if (f.imapIdVersion.trim()) cfg.idVersion = f.imapIdVersion.trim();
  if (f.imapIdVendor.trim()) cfg.idVendor = f.imapIdVendor.trim();
  if (f.imapIdSupportUrl.trim()) cfg.idSupportUrl = f.imapIdSupportUrl.trim();
  if (f.imapDisableRev2) cfg.disableRev2 = true;
  return cfg;
};

const buildNetwork = (f: AccountFormModel): NetworkW57 | undefined => {
  const addrs = splitPaths(f.netLocalAddresses);
  const net: NetworkW57 = {};
  if (f.netProxy.trim()) net.proxy = f.netProxy.trim();
  if (f.netLocalAddress.trim()) net.localAddress = f.netLocalAddress.trim();
  if (f.netEhloName.trim()) net.ehloName = f.netEhloName.trim();
  if (addrs.length) net.localAddresses = addrs;
  if (f.netSmtpStrategy.trim()) net.smtpStrategy = f.netSmtpStrategy.trim();
  if (f.netImapStrategy.trim()) net.imapStrategy = f.netImapStrategy.trim();
  return Object.keys(net).length ? net : undefined;
};

const buildSmtp = (f: AccountFormModel, includePass: boolean): SMTPConfigInput => {
  const auth: SMTPConfigInput["auth"] = { user: f.smtpUser.trim() };
  if (includePass && f.smtpPass) auth.pass = f.smtpPass;
  const cfg: SMTPConfigInput = {
    host: f.smtpHost.trim(),
    port: Number(f.smtpPort),
    tls: f.smtpTls,
    auth,
    policy: f.smtpPolicy,
  };
  if (f.smtpMaxAttempts != null && f.smtpMaxAttempts > 0) cfg.maxAttempts = f.smtpMaxAttempts;
  if (f.smtpBackoffBase.trim()) cfg.backoffBase = f.smtpBackoffBase.trim();
  return cfg;
};

const buildSync = (f: AccountFormModel): AccountsSyncConfig => {
  const sync: AccountsSyncConfig = {
    paths: splitPaths(f.syncPaths),
    idlePaths: splitPaths(f.idlePaths),
  };
  const notifyFrom = toIsoOrUndef(f.notifyFrom);
  const syncFrom = toIsoOrUndef(f.syncFrom);
  if (notifyFrom) sync.notifyFrom = notifyFrom;
  if (syncFrom) sync.syncFrom = syncFrom;
  if (f.resyncDelay.trim()) sync.resyncDelay = f.resyncDelay.trim();
  if (f.maxIdle.trim()) sync.maxIdle = f.maxIdle.trim();
  return sync;
};

/** Create payload. Passwords are required on create (caller validates). */
export const toCreateInput = (f: AccountFormModel): CreateInput => {
  const body: CreateInput = {
    name: f.name.trim(),
    email: f.email.trim(),
    imap: buildImap(f, true),
    smtp: buildSmtp(f, true),
    sync: buildSync(f),
  };
  if (f.externalId.trim()) body.externalId = f.externalId.trim();
  if (f.tz.trim()) body.tz = f.tz.trim();
  if (f.locale.trim()) body.locale = f.locale.trim();
  const net = buildNetwork(f);
  if (net) (body as CreateInput & { network?: NetworkW57 }).network = net;
  const switches: Record<string, boolean> = {};
  if (f.ignoreCertErrors) switches["accounts.ignoreCertErrors"] = true;
  if (f.pecEnabled) switches["pec.enabled"] = true;
  if (Object.keys(switches).length > 0) body.switches = switches;
  const metadata = metadataFromRows(f.metadata);
  if (Object.keys(metadata).length > 0) (body as CreateInput & { metadata?: AccountMetadata }).metadata = metadata;
  return body;
};

/**
 * Patch payload. Omits passwords unless the user typed a new one. `metadataBefore` is the stored metadata:
 * metadata is sent as a JSON Merge Patch against it (removed keys as null), and only when it changed.
 */
export const toPatchBody = (
  f: AccountFormModel,
  metadataBefore?: AccountMetadata | null,
): AccountsPatchBody => {
  const body: AccountsPatchBody = {
    name: f.name.trim(),
    email: f.email.trim(),
    imap: buildImap(f, Boolean(f.imapPass)),
    smtp: buildSmtp(f, Boolean(f.smtpPass)),
    sync: buildSync(f),
    switches: {
      "accounts.ignoreCertErrors": f.ignoreCertErrors,
      ...(f.pecEnabled ? { "pec.enabled": true } : {}),
    },
  };
  body.externalId = f.externalId.trim() || null;
  if (f.tz.trim()) body.tz = f.tz.trim();
  if (f.locale.trim()) body.locale = f.locale.trim();
  const net = buildNetwork(f);
  if (net) body.network = net;
  else (body as { network?: NetworkW57 | null }).network = null;
  const metadataPatch = metadataMergePatch(metadataBefore, metadataFromRows(f.metadata));
  if (metadataPatch) (body as { metadata?: AccountMetadata }).metadata = metadataPatch;
  return body;
};

export const toVerifyRequest = (f: AccountFormModel, isEdit: boolean): VerifyRequest => ({
  ignoreCertErrors: f.ignoreCertErrors,
  imap: buildImap(f, !isEdit || Boolean(f.imapPass)),
  smtp: buildSmtp(f, !isEdit || Boolean(f.smtpPass)),
});

/** Localise host/port rule messages (rules return sentinel keys). */
export const localiseRule =
  (t: (key: string) => string) =>
  (rules: ValidationRule[]): ValidationRule[] =>
    rules.map(
      (rule) => (value: unknown) => {
        const r = rule(value);
        if (r === "invalid_host") return t("accounts.err_host");
        if (r === "invalid_port") return t("accounts.err_port");
        return r;
      },
    );
