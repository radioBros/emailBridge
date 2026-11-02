import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h, nextTick, reactive, ref } from "vue";
import { enableAutoUnmount, flushPromises, mount } from "@vue/test-utils";
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

const rt = vi.hoisted(() => ({
  opts: null as null | { onMessage?: (m: unknown) => void; onReconnect?: () => void; subscribe?: unknown },
  status: null as unknown,
}));
vi.mock("@/composables/useRealtime", async () => {
  const { ref: vref } = await import("vue");
  rt.status = vref("open");
  return {
    useRealtime: (opts: typeof rt.opts) => {
      rt.opts = opts;
      return { status: rt.status, lastMessage: vref(null), client: {} };
    },
  };
});

const push = vi.fn();
vi.mock("vue-router", () => ({
  useRouter: () => ({
    push,
    resolve: (p: string) => ({ matched: p.startsWith("/accounts") ? [{ path: "/accounts/:id" }] : [] }),
  }),
}));

const session = vi.hoisted(() => ({ value: null as unknown }));
vi.mock("@/stores/session", () => ({ useSessionStore: () => session.value }));

import { useNotifyStore } from "@/stores/notify";
import { FALLBACK_POLL_MS, useNotificationBell } from "@/features/notifications/composables/useNotificationBell";

const n = (id: string, over: Partial<Notification> = {}): Notification => ({
  id,
  scope: "org_1",
  key: `k:${id}`,
  category: "accounts",
  severity: "critical",
  state: "open",
  titleKey: "notify.account.auth.title",
  detailKey: "notify.account.auth.detail",
  link: "/accounts/acc_1",
  occurrences: 1,
  firstSeen: "2026-10-27T10:00:00Z",
  lastSeen: "2026-10-27T10:00:00Z",
  unread: true,
  ...over,
});

const summary = (critical: number, warning = 0) => ({
  unread: critical + warning,
  open: critical + warning,
  bySeverity: { critical, warning, info: 0 },
});

let bell!: ReturnType<typeof useNotificationBell>;
const Host = defineComponent({
  setup() {
    bell = useNotificationBell();
    return () => h("div");
  },
});

beforeEach(() => {
  vi.useFakeTimers();
  for (const f of Object.values(api)) f.mockReset();
  push.mockReset();
  session.value = reactive({ organizationId: "org_1", me: { role: "owner", permissions: [] } });
  (rt.status as { value: string }).value = "open";
});

enableAutoUnmount(afterEach);

afterEach(() => {
  vi.useRealTimers();
});

describe("useNotificationBell", () => {
  it("primes on boot without toasting existing criticals", async () => {
    api.getNotificationsSummary.mockResolvedValue(summary(1));
    api.listNotifications.mockResolvedValue({ data: [n("a")] });
    mount(Host);
    await flushPromises();
    expect(rt.opts?.subscribe).toEqual({ id: "notifications-bell", topic: "notifications" });
    expect(bell.badge.value).toBe("1");
    expect(bell.badgeColor.value).toBe("error");
    expect(bell.ariaLabel.value).toBe("notifications.bell_label_unread");
    expect(useNotifyStore().items).toHaveLength(0);
  });

  it("toasts a new critical once after a realtime raise", async () => {
    api.getNotificationsSummary.mockResolvedValue(summary(0));
    mount(Host);
    await flushPromises();

    api.getNotificationsSummary.mockResolvedValue(summary(1));
    api.listNotifications.mockResolvedValue({ data: [n("a")] });
    const frame = { topic: "notifications", type: "notification.raised", data: { key: "k:a", severity: "critical", state: "open" } };
    rt.opts?.onMessage?.(frame);
    rt.opts?.onMessage?.(frame);
    await vi.advanceTimersByTimeAsync(400);
    await flushPromises();
    const notify = useNotifyStore();
    expect(notify.items).toHaveLength(1);
    expect(notify.items[0]!.color).toBe("error");

    rt.opts?.onMessage?.({ ...frame, type: "notification.updated" });
    await vi.advanceTimersByTimeAsync(400);
    await flushPromises();
    expect(notify.items).toHaveLength(1);
  });

  it("does not toast an open critical the user already read when the watcher bumps it", async () => {
    api.getNotificationsSummary.mockResolvedValue({ unread: 0, open: 1, bySeverity: { critical: 0, warning: 0, info: 0 } });
    api.listNotifications.mockResolvedValue({ data: [n("a", { unread: false })] });
    mount(Host);
    await flushPromises();
    rt.opts?.onMessage?.({
      topic: "notifications",
      type: "notification.updated",
      data: { key: "k:a", severity: "critical", state: "open" },
    });
    await vi.advanceTimersByTimeAsync(400);
    await flushPromises();
    expect(useNotifyStore().items).toHaveLength(0);
  });

  it("ignores other topics and shows no badge for a junk summary", async () => {
    api.getNotificationsSummary.mockResolvedValue({ data: [], total: 7 });
    mount(Host);
    await flushPromises();
    expect(bell.badge.value).toBe("");
    expect(bell.badgeColor.value).toBe("primary");
    rt.opts?.onMessage?.({ topic: "accounts", type: "account.updated" });
    await vi.advanceTimersByTimeAsync(400);
    expect(api.getNotificationsSummary).toHaveBeenCalledTimes(1);
  });

  it("polls only while the socket is down", async () => {
    api.getNotificationsSummary.mockResolvedValue(summary(0));
    mount(Host);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS + 10);
    expect(api.getNotificationsSummary).toHaveBeenCalledTimes(1);

    (rt.status as { value: string }).value = "closed";
    await nextTick();
    await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS + 10);
    expect(api.getNotificationsSummary).toHaveBeenCalledTimes(2);

    (rt.status as { value: string }).value = "open";
    await nextTick();
    await vi.advanceTimersByTimeAsync(FALLBACK_POLL_MS * 2);
    expect(api.getNotificationsSummary).toHaveBeenCalledTimes(2);
  });

  it("refetches on reconnect and on organisation switch", async () => {
    api.getNotificationsSummary.mockResolvedValue(summary(0));
    mount(Host);
    await flushPromises();
    rt.opts?.onReconnect?.();
    await flushPromises();
    expect(api.getNotificationsSummary).toHaveBeenCalledTimes(2);
    (session.value as { organizationId: string }).organizationId = "org_2";
    await nextTick();
    await flushPromises();
    expect(api.getNotificationsSummary).toHaveBeenCalledTimes(3);
  });

  it("opens the panel, navigates on select and marks read", async () => {
    api.getNotificationsSummary.mockResolvedValue(summary(0));
    api.listNotifications.mockResolvedValue({ data: [n("a")] });
    mount(Host);
    await flushPromises();
    bell.open.value = true;
    await nextTick();
    await flushPromises();
    expect(bell.feed.rows).toHaveLength(2);

    await bell.select(n("a"));
    expect(push).toHaveBeenCalledWith("/accounts/acc_1");
    expect(bell.open.value).toBe(false);
    expect(api.markNotificationsRead).toHaveBeenCalledWith(["a"]);

    push.mockReset();
    await bell.select(n("b", { link: "//evil.test", unread: false }));
    expect(push).not.toHaveBeenCalled();
  });

  it("reports failed user actions", async () => {
    api.getNotificationsSummary.mockResolvedValue(summary(0));
    mount(Host);
    await flushPromises();
    api.markAllNotificationsRead.mockRejectedValue(new Error("down"));
    await bell.markAllRead();
    api.markNotificationsRead.mockRejectedValue(new Error("down"));
    await bell.select(n("z", { link: undefined }));
    api.listNotifications.mockRejectedValue(new Error("down"));
    await bell.retryPanel();
    const notify = useNotifyStore();
    expect(notify.items.map((i) => i.color)).toEqual(["error", "error", "error"]);
    expect(bell.markingAll.value).toBe(false);
    bell.openCentre();
    expect(push).toHaveBeenCalledWith("/notifications");
  });

  it("keeps quiet when a background refresh fails", async () => {
    api.getNotificationsSummary.mockRejectedValue(new Error("down"));
    mount(Host);
    await flushPromises();
    expect(useNotifyStore().items).toHaveLength(0);
    expect(ref(bell.unread.value).value).toBe(0);
  });
});
