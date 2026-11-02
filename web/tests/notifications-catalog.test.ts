import { describe, expect, it } from "vitest";
import { createI18n } from "vue-i18n";
import { createMemoryHistory, createRouter } from "vue-router";
import en from "@/features/notifications/locales/en.json";
import type { Notification } from "@/api/notifications";
import {
  badgeText,
  canResolve,
  categorySpec,
  detailOf,
  groupForPanel,
  highestSeverity,
  isSafeLink,
  isSeverity,
  normalizeSummary,
  routeForLink,
  severitySpec,
  sortForPanel,
  titleOf,
} from "@/features/notifications/helpers/catalog";

const n = (over: Partial<Notification>): Notification => ({
  id: "ntf_x",
  scope: "org_1",
  key: "k",
  category: "webhooks",
  severity: "warning",
  state: "open",
  titleKey: "notify.webhook.failing.title",
  detailKey: "notify.webhook.failing.detail",
  occurrences: 1,
  firstSeen: "2026-10-27T10:00:00Z",
  lastSeen: "2026-10-27T10:00:00Z",
  ...over,
});

describe("summary helpers", () => {
  it("normalizes junk to zeros and never NaN", () => {
    expect(normalizeSummary({ data: [], total: 7 })).toEqual({
      unread: 0,
      open: 0,
      bySeverity: { critical: 0, warning: 0, info: 0 },
    });
    expect(normalizeSummary(null).unread).toBe(0);
    expect(normalizeSummary({ unread: -3, open: "x", bySeverity: { critical: 2.7 } })).toEqual({
      unread: 0,
      open: 0,
      bySeverity: { critical: 2, warning: 0, info: 0 },
    });
  });

  it("picks the highest severity", () => {
    expect(highestSeverity(normalizeSummary({ bySeverity: { info: 1, warning: 2 } }))).toBe("warning");
    expect(highestSeverity(normalizeSummary({ bySeverity: { info: 1, critical: 1 } }))).toBe("critical");
    expect(highestSeverity(normalizeSummary({}))).toBeNull();
  });

  it("caps the badge", () => {
    expect(badgeText(7)).toBe("7");
    expect(badgeText(100)).toBe("99+");
  });

  it("maps specs with fallbacks", () => {
    expect(categorySpec("webhooks").color).toBe("icon-violet");
    expect(categorySpec("nope").icon).toBeTruthy();
    expect(severitySpec("critical").color).toBe("error");
    expect(severitySpec("weird")).toBe(severitySpec("info"));
    expect(isSeverity("info")).toBe(true);
    expect(isSeverity("loud")).toBe(false);
  });
});

describe("panel ordering", () => {
  it("puts open first by severity, newest first, resolved last", () => {
    const rows = [
      n({ id: "r", state: "resolved", severity: "critical", lastSeen: "2026-10-27T12:00:00Z" }),
      n({ id: "i", severity: "info", lastSeen: "2026-10-27T12:00:00Z" }),
      n({ id: "w1", severity: "warning", lastSeen: "2026-10-27T09:00:00Z" }),
      n({ id: "c", severity: "critical", lastSeen: "2026-10-27T08:00:00Z" }),
      n({ id: "w2", severity: "warning", lastSeen: "2026-10-27T11:00:00Z" }),
    ];
    expect(sortForPanel(rows).map((r) => r.id)).toEqual(["c", "w2", "w1", "i", "r"]);
    const groups = groupForPanel(rows);
    expect(groups.map((g) => g.key)).toEqual(["critical", "warning", "info", "resolved"]);
    expect(groupForPanel([n({ severity: "info" })]).map((g) => g.key)).toEqual(["info"]);
  });
});

describe("text", () => {
  const i18n = createI18n({ legacy: false, locale: "en", messages: { en } });
  const t = i18n.global.t as (k: string, p?: Record<string, unknown>) => string;
  const te = i18n.global.te as (k: string) => boolean;

  it("renders the translated title with params", () => {
    expect(titleOf(n({ titleParams: { name: "CRM" } }), t, te)).toBe("Webhook CRM is failing");
    expect(detailOf(n({ detailParams: { failures: "12" } }), t, te)).toContain("12 deliveries");
  });

  it("never shows a raw placeholder when params are missing", () => {
    expect(titleOf(n({}), t, te)).not.toContain("{name}");
  });

  it("humanizes unknown keys and returns empty detail", () => {
    expect(titleOf(n({ titleKey: "notify.storage.bucketFull.title" }), t, te)).toBe("Storage bucket full");
    expect(detailOf(n({ detailKey: "notify.none.detail" }), t, te)).toBe("");
  });
});

describe("links", () => {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: "/",
        component: { template: "<div />" },
        children: [
          { path: "webhooks", component: { template: "<div />" } },
          { path: "accounts/:id", component: { template: "<div />" } },
        ],
      },
      { path: "/:pathMatch(.*)*", redirect: "/" },
    ],
  });

  it("accepts relative admin paths only", () => {
    expect(isSafeLink("/webhooks")).toBe(true);
    expect(isSafeLink("//evil.test")).toBe(false);
    expect(isSafeLink("https://evil.test")).toBe(false);
    expect(isSafeLink("/\\evil")).toBe(false);
    expect(isSafeLink(undefined)).toBe(false);
  });

  it("keeps known routes and falls back to the nearest parent", () => {
    expect(routeForLink(router, "/accounts/acc_1")).toBe("/accounts/acc_1");
    expect(routeForLink(router, "/webhooks/whe_1")).toBe("/webhooks");
    expect(routeForLink(router, "/organization/api-keys")).toBeNull();
    expect(routeForLink(router, "//evil.test")).toBeNull();
  });
});

describe("canResolve", () => {
  it("gates by permission and severity", () => {
    const admin = { permissions: ["read", "settings.write"] };
    const operator = { permissions: ["read", "webhooks.write"] };
    const viewer = { permissions: ["read"] };
    expect(canResolve(n({ severity: "critical" }), admin)).toBe(true);
    expect(canResolve(n({ severity: "info" }), operator)).toBe(true);
    expect(canResolve(n({ severity: "warning" }), operator)).toBe(false);
    expect(canResolve(n({ severity: "info" }), viewer)).toBe(false);
    expect(canResolve(n({ state: "resolved" }), admin)).toBe(false);
    expect(canResolve(n({}), null)).toBe(false);
  });
});
