import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Notification } from "@/api/notifications";

const api = vi.hoisted(() => ({
  getNotificationsSummary: vi.fn(),
  listNotifications: vi.fn(),
  markNotificationsRead: vi.fn(async () => undefined),
  markAllNotificationsRead: vi.fn(async () => undefined),
  resolveNotification: vi.fn(),
}));

vi.mock("@/api/notifications", async (orig) => ({
  ...(await orig<typeof import("@/api/notifications")>()),
  ...api,
}));

import { realtimeNotificationOf, useNotificationFeed } from "@/features/notifications/stores/feed";

const n = (id: string, over: Partial<Notification> = {}): Notification => ({
  id,
  scope: "org_1",
  key: `k:${id}`,
  category: "queues",
  severity: "critical",
  state: "open",
  titleKey: "t",
  detailKey: "d",
  occurrences: 1,
  firstSeen: "2026-10-27T10:00:00Z",
  lastSeen: "2026-10-27T10:00:00Z",
  unread: true,
  ...over,
});

beforeEach(() => {
  for (const f of Object.values(api)) f.mockReset();
  api.getNotificationsSummary.mockResolvedValue({ unread: 2, open: 2, bySeverity: { critical: 1, warning: 1, info: 0 } });
});

describe("realtimeNotificationOf", () => {
  it("only accepts notification frames on the notifications topic", () => {
    expect(realtimeNotificationOf({ topic: "accounts", type: "notification.raised" })).toBeNull();
    expect(realtimeNotificationOf({ topic: "notifications", type: "other" })).toBeNull();
    expect(realtimeNotificationOf({ topic: "notifications", type: "notification.raised", data: { key: "a" } })).toEqual({
      key: "a",
    });
    expect(realtimeNotificationOf({ topic: "notifications", type: "notification.resolved" })).toEqual({});
    expect(realtimeNotificationOf({ id: "notifications-bell", type: "notification.updated", data: {} })).toEqual({});
  });
});

describe("notification feed", () => {
  it("refreshes a normalized summary", async () => {
    const feed = useNotificationFeed();
    await feed.refreshSummary();
    expect(feed.summary.unread).toBe(2);
    expect(feed.topSeverity).toBe("critical");
    expect(feed.summaryLoaded).toBe(true);
  });

  it("loads open + resolved rows and groups them", async () => {
    api.listNotifications
      .mockResolvedValueOnce({ data: [n("a"), n("b", { severity: "info" })] })
      .mockResolvedValueOnce({ data: [n("c", { state: "resolved" })] });
    const feed = useNotificationFeed();
    await feed.loadPanel();
    expect(api.listNotifications).toHaveBeenNthCalledWith(1, expect.objectContaining({ state: "open" }));
    expect(api.listNotifications).toHaveBeenNthCalledWith(2, expect.objectContaining({ state: "resolved" }));
    expect(feed.groups.map((g) => g.key)).toEqual(["critical", "info", "resolved"]);
    expect(feed.panelLoadedOnce).toBe(true);
  });

  it("keeps the error and stays a first load when the panel fails", async () => {
    api.listNotifications.mockRejectedValue(new Error("boom"));
    const feed = useNotificationFeed();
    await expect(feed.loadPanel()).rejects.toThrow("boom");
    expect(feed.panelError).toBeTruthy();
    expect(feed.panelLoadedOnce).toBe(false);
  });

  it("primes criticals silently, then reports only new keys once", async () => {
    const feed = useNotificationFeed();
    api.listNotifications.mockResolvedValueOnce({ data: [n("a")] });
    expect(await feed.freshCriticals()).toEqual([]);
    expect(feed.isPrimed()).toBe(true);
    api.listNotifications.mockResolvedValueOnce({ data: [n("a"), n("b")] });
    expect((await feed.freshCriticals()).map((x) => x.id)).toEqual(["b"]);
    api.listNotifications.mockResolvedValueOnce({ data: [n("a"), n("b")] });
    expect(await feed.freshCriticals()).toEqual([]);
    expect(feed.isKnownCritical("k:b")).toBe(true);
  });

  it("primes without a probe when nothing critical is open", () => {
    const feed = useNotificationFeed();
    feed.primeIfQuiet({ unread: 1, open: 1, bySeverity: { critical: 1, warning: 0, info: 0 } });
    expect(feed.isPrimed()).toBe(false);
    // Open but already read: bySeverity is zero, yet the critical must still be probed.
    feed.primeIfQuiet({ unread: 0, open: 1, bySeverity: { critical: 0, warning: 0, info: 0 } });
    expect(feed.isPrimed()).toBe(false);
    feed.primeIfQuiet({ unread: 0, open: 0, bySeverity: { critical: 0, warning: 0, info: 0 } });
    expect(feed.isPrimed()).toBe(true);
    feed.markKnown("x");
    expect(feed.isKnownCritical("x")).toBe(true);
  });

  it("marks read (skipping rows already read), all read and resolves", async () => {
    api.listNotifications
      .mockResolvedValueOnce({ data: [n("a"), n("b", { unread: false })] })
      .mockResolvedValueOnce({ data: [] });
    const feed = useNotificationFeed();
    await feed.loadPanel();
    const rev = feed.revision;
    await feed.markRead(["a", "b"]);
    expect(api.markNotificationsRead).toHaveBeenCalledWith(["a"]);
    expect(feed.rows.every((r) => r.unread === false)).toBe(true);
    expect(feed.revision).toBe(rev + 1);
    await feed.markRead(["b"]);
    expect(api.markNotificationsRead).toHaveBeenCalledTimes(1);

    await feed.markAllRead();
    expect(api.markAllNotificationsRead).toHaveBeenCalled();

    api.resolveNotification.mockResolvedValue(n("a", { state: "resolved" }));
    const out = await feed.resolve("a");
    expect(out.state).toBe("resolved");
    expect(feed.rows.find((r) => r.id === "a")?.state).toBe("resolved");
  });

  it("resets everything on organisation switch", async () => {
    const feed = useNotificationFeed();
    await feed.refreshSummary();
    feed.markKnown("x");
    feed.touch();
    feed.reset();
    expect(feed.summary.unread).toBe(0);
    expect(feed.summaryLoaded).toBe(false);
    expect(feed.isKnownCritical("x")).toBe(false);
    expect(feed.isPrimed()).toBe(false);
  });
});
