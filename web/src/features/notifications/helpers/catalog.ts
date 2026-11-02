import {
  mdiAccountAlertOutline,
  mdiAlertCircle,
  mdiAlertOctagon,
  mdiBugOutline,
  mdiCertificateOutline,
  mdiDatabaseOutline,
  mdiGaugeFull,
  mdiInformation,
  mdiKeyChainVariant,
  mdiPuzzleOutline,
  mdiSendClockOutline,
  mdiServerNetwork,
  mdiShieldAlertOutline,
  mdiTrayFull,
  mdiWebhook,
} from "@mdi/js";
import type { Router } from "vue-router";
import type { IconPaletteKey } from "@/core/config/IconColors";
import type { StatusColor } from "@/constants/Statuses";
import {
  NOTIFICATION_SEVERITIES,
  type Notification,
  type NotificationCategory,
  type NotificationSeverity,
  type NotificationSummary,
} from "@/api/notifications";

export interface CategorySpec {
  icon: string;
  /** Central icon palette token (core/config/IconColors). */
  color: IconPaletteKey;
}

/** Category → icon + palette colour. One place, used by the bell, the centre and preferences. */
export const CATEGORY_SPECS: Record<NotificationCategory, CategorySpec> = {
  webhooks: { icon: mdiWebhook, color: "icon-violet" },
  queues: { icon: mdiTrayFull, color: "icon-rose" },
  accounts: { icon: mdiAccountAlertOutline, color: "icon-cyan" },
  sending: { icon: mdiSendClockOutline, color: "icon-teal" },
  oauth: { icon: mdiKeyChainVariant, color: "icon-amber" },
  integrations: { icon: mdiPuzzleOutline, color: "icon-rose" },
  pec: { icon: mdiCertificateOutline, color: "icon-cyan" },
  storage: { icon: mdiDatabaseOutline, color: "icon-indigo" },
  platform: { icon: mdiServerNetwork, color: "icon-rose" },
  security: { icon: mdiShieldAlertOutline, color: "icon-amber" },
  limits: { icon: mdiGaugeFull, color: "icon-teal" },
  internal: { icon: mdiBugOutline, color: "icon-indigo" },
};

const FALLBACK_CATEGORY: CategorySpec = { icon: mdiInformation, color: "icon-indigo" };

export const categorySpec = (category: string): CategorySpec =>
  CATEGORY_SPECS[category as NotificationCategory] ?? FALLBACK_CATEGORY;

export interface SeveritySpec {
  color: StatusColor;
  icon: string;
  rank: number;
}

export const SEVERITY_SPECS: Record<NotificationSeverity, SeveritySpec> = {
  critical: { color: "error", icon: mdiAlertOctagon, rank: 3 },
  warning: { color: "warning", icon: mdiAlertCircle, rank: 2 },
  info: { color: "info", icon: mdiInformation, rank: 1 },
};

export const severitySpec = (severity: string): SeveritySpec =>
  SEVERITY_SPECS[severity as NotificationSeverity] ?? SEVERITY_SPECS.info;

export const isSeverity = (v: unknown): v is NotificationSeverity =>
  typeof v === "string" && (NOTIFICATION_SEVERITIES as readonly string[]).includes(v);

const count = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) && v > 0 ? Math.floor(v) : 0);

/**
 * A summary the bell can trust. Anything malformed (a mocked or failing endpoint answering with
 * another shape) degrades to "nothing unread" instead of a NaN badge.
 */
export const normalizeSummary = (raw: unknown): NotificationSummary => {
  const r = (raw && typeof raw === "object" ? raw : {}) as Partial<NotificationSummary>;
  const by = (r.bySeverity && typeof r.bySeverity === "object" ? r.bySeverity : {}) as Partial<
    Record<NotificationSeverity, unknown>
  >;
  return {
    unread: count(r.unread),
    open: count(r.open),
    bySeverity: { critical: count(by.critical), warning: count(by.warning), info: count(by.info) },
  };
};

/** Highest severity with unread items, else with open items; null when all is quiet. */
export const highestSeverity = (s: NotificationSummary): NotificationSeverity | null => {
  for (const sev of NOTIFICATION_SEVERITIES) {
    if (s.bySeverity[sev] > 0) return sev;
  }
  return null;
};

/** Badge text: counts above 99 collapse so the badge never widens the top bar. */
export const badgeText = (n: number): string => (n > 99 ? "99+" : String(n));

/** Open first (critical → warning → info, newest first inside a group), then resolved, newest first. */
export const sortForPanel = (rows: Notification[]): Notification[] =>
  [...rows].sort((a, b) => {
    const ao = a.state === "open" ? 1 : 0;
    const bo = b.state === "open" ? 1 : 0;
    if (ao !== bo) return bo - ao;
    if (ao) {
      const r = severitySpec(b.severity).rank - severitySpec(a.severity).rank;
      if (r !== 0) return r;
    }
    return Date.parse(b.lastSeen || "") - Date.parse(a.lastSeen || "") || 0;
  });

export interface PanelGroup {
  key: NotificationSeverity | "resolved";
  items: Notification[];
}

/** Panel sections in display order; empty groups are dropped. */
export const groupForPanel = (rows: Notification[]): PanelGroup[] => {
  const sorted = sortForPanel(rows);
  const groups: PanelGroup[] = [
    ...NOTIFICATION_SEVERITIES.map((sev) => ({
      key: sev,
      items: sorted.filter((n) => n.state === "open" && severitySpec(n.severity) === SEVERITY_SPECS[sev]),
    })),
    { key: "resolved" as const, items: sorted.filter((n) => n.state !== "open") },
  ];
  return groups.filter((g) => g.items.length > 0);
};

type Translate = (key: string, params?: Record<string, unknown>) => string;
type Exists = (key: string) => boolean;

/** "notify.webhook.failing.title" → "Webhook failing" when the key has no translation yet. */
const humanizeKey = (key: string): string => {
  const parts = key.split(".").filter((p) => p !== "notify" && p !== "title" && p !== "detail");
  const text = parts.join(" ").replace(/([a-z])([A-Z])/g, "$1 $2").toLowerCase();
  return text ? text.charAt(0).toUpperCase() + text.slice(1) : key;
};

/** Translated title; params missing from a realtime payload render as empty, never as `{name}`. */
export const titleOf = (n: Pick<Notification, "titleKey" | "titleParams">, t: Translate, te: Exists): string =>
  n.titleKey && te(n.titleKey) ? t(n.titleKey, { ...(n.titleParams ?? {}) }) : humanizeKey(n.titleKey || "");

export const detailOf = (n: Pick<Notification, "detailKey" | "detailParams">, t: Translate, te: Exists): string =>
  n.detailKey && te(n.detailKey) ? t(n.detailKey, { ...(n.detailParams ?? {}) }) : "";

/** Relative admin paths only: no scheme, no protocol-relative `//`, no backslash tricks. */
export const isSafeLink = (link: string | undefined): link is string =>
  typeof link === "string" && link.startsWith("/") && !link.startsWith("//") && !link.includes("\\");

/**
 * The admin route a notification points at, or null. A link the router does not know (the catch-all
 * would bounce to the dashboard) falls back to its nearest known parent, e.g. `/webhooks/whe_…` →
 * `/webhooks`.
 */
export const routeForLink = (router: Router, link: string | undefined): string | null => {
  if (!isSafeLink(link)) return null;
  const path = link.split(/[?#]/)[0] ?? "";
  const segments = path.split("/").filter(Boolean);
  for (let n = segments.length; n > 0; n -= 1) {
    const candidate = `/${segments.slice(0, n).join("/")}`;
    const resolved = router.resolve(candidate);
    const real = resolved.matched.length > 0 && !resolved.matched.some((m) => m.path.includes("pathMatch"));
    if (real) return n === segments.length ? link : candidate;
  }
  return null;
};

export interface Permissions {
  permissions?: string[] | null;
}

/**
 * Manual resolve. The server accepts `settings.write` or `webhooks.write`; per the spec warning and
 * critical problems stay with owners / admins (`settings.write`), so the UI only offers those.
 */
export const canResolve = (n: Pick<Notification, "severity" | "state">, me: Permissions | null | undefined): boolean => {
  if (n.state !== "open") return false;
  const perms = new Set(me?.permissions ?? []);
  if (perms.has("settings.write")) return true;
  return n.severity === "info" && perms.has("webhooks.write");
};
