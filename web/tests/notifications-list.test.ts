import { beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import type { Notification } from "@/api/notifications";

const api = vi.hoisted(() => ({
  getNotificationsSummary: vi.fn(async () => ({ unread: 0, open: 0, bySeverity: { critical: 0, warning: 0, info: 0 } })),
  listNotifications: vi.fn(),
  markNotificationsRead: vi.fn(async () => undefined),
  markAllNotificationsRead: vi.fn(async () => undefined),
  resolveNotification: vi.fn(),
}));
vi.mock("@/api/notifications", async (orig) => ({
  ...(await orig<typeof import("@/api/notifications")>()),
  ...api,
}));

const replace = vi.fn();
const route = vi.hoisted(() => ({ query: {} as Record<string, string> }));
vi.mock("vue-router", () => ({
  useRoute: () => route,
  useRouter: () => ({ replace, push: vi.fn(), resolve: () => ({ matched: [] }) }),
}));

const session = vi.hoisted(() => ({ value: null as unknown }));
vi.mock("@/stores/session", () => ({ useSessionStore: () => session.value }));

import { useNotifyStore } from "@/stores/notify";
import { sortParam, useNotificationsList } from "@/features/notifications/composables/useNotificationsList";
import NotificationPanel from "@/features/notifications/components/NotificationPanel.vue";
import { groupForPanel } from "@/features/notifications/helpers/catalog";

const n = (id: string, over: Partial<Notification> = {}): Notification => ({
  id,
  scope: "org_1",
  key: `k:${id}`,
  category: "webhooks",
  severity: "info",
  state: "open",
  titleKey: "t",
  detailKey: "d",
  occurrences: 3,
  firstSeen: "2026-10-27T10:00:00Z",
  lastSeen: "2026-10-27T10:00:00Z",
  unread: true,
  ...over,
});

let list!: ReturnType<typeof useNotificationsList>;
const Host = defineComponent({
  setup() {
    list = useNotificationsList();
    return () => h("div");
  },
});

beforeEach(() => {
  api.listNotifications.mockReset();
  api.resolveNotification.mockReset();
  replace.mockReset();
  route.query = {};
  session.value = { organizationId: "org_1", me: { role: "admin", permissions: ["read", "settings.write"] } };
});

describe("sortParam", () => {
  it("whitelists date sorts", () => {
    expect(sortParam([])).toBe("-lastSeen");
    expect(sortParam([{ key: "firstSeen", order: "asc" }])).toBe("firstSeen");
    expect(sortParam([{ key: "lastSeen", order: "desc" }])).toBe("-lastSeen");
    expect(sortParam([{ key: "severity", order: "desc" }])).toBe("-lastSeen");
  });
});

describe("useNotificationsList", () => {
  it("reads filters from the URL and sends them server-side", async () => {
    route.query = { state: "resolved", category: "queues", severity: "warning", unread: "1", page: "2" };
    api.listNotifications.mockResolvedValue({ data: [n("a")], total: 30 });
    mount(Host);
    await list.load();
    expect(api.listNotifications).toHaveBeenCalledWith(
      expect.objectContaining({ state: "resolved", category: "queues", severity: "warning", unread: true, page: 2, includeTotal: true }),
    );
    expect(list.total.value).toBe(30);
    expect(list.activeFilterCount.value).toBe(4);
    expect(replace).toHaveBeenCalledWith({
      query: { page: "2", state: "resolved", category: "queues", severity: "warning", unread: "1" },
    });
    list.clearFilters();
    await flushPromises();
    expect(list.activeFilterCount.value).toBe(0);
  });

  it("ignores unknown URL values", async () => {
    route.query = { category: "nope", severity: "loud", state: "weird" };
    api.listNotifications.mockResolvedValue({ data: [] });
    mount(Host);
    await list.load();
    expect(list.filterCategory.value).toBeNull();
    expect(list.filterSeverity.value).toBeNull();
    expect(list.filterState.value).toBe("open");
    expect(list.empty.value).toBe(true);
  });

  it("selects, bulk-reads and bulk-resolves info only with partial failure", async () => {
    api.listNotifications.mockResolvedValue({
      data: [n("a"), n("b"), n("w", { severity: "warning" }), n("r", { unread: false })],
      total: 4,
    });
    mount(Host);
    await list.load();
    list.toggleAllOnPage(true);
    expect(list.allOnPageSelected.value).toBe(true);
    expect(list.selectedResolvable.value.map((x) => x.id)).toEqual(["a", "b", "r"]);
    expect(list.selectedUnread.value.map((x) => x.id)).toEqual(["a", "b", "w"]);

    await list.bulkMarkRead();
    expect(api.markNotificationsRead).toHaveBeenCalledWith(["a", "b", "w"]);
    expect(list.selected.value).toEqual([]);

    list.toggle("a", true);
    list.toggle("b", true);
    list.toggle("w", true);
    expect(list.someOnPageSelected.value).toBe(true);
    api.resolveNotification.mockResolvedValueOnce(n("a", { state: "resolved" })).mockRejectedValueOnce(new Error("x"));
    await list.bulkResolve();
    expect(api.resolveNotification).toHaveBeenCalledTimes(2);
    const colors = useNotifyStore().items.map((i) => i.color);
    expect(colors).toContain("success");
    expect(colors).toContain("error");
  });

  it("refuses bulk resolve for viewers", async () => {
    session.value = { organizationId: "org_1", me: { role: "viewer", permissions: ["read"] } };
    api.listNotifications.mockResolvedValue({ data: [n("a")] });
    mount(Host);
    await list.load();
    list.toggle("a", true);
    expect(list.selectedResolvable.value).toEqual([]);
    await list.bulkResolve();
    expect(api.resolveNotification).not.toHaveBeenCalled();
    list.toggle("a", false);
    expect(list.selected.value).toEqual([]);
  });

  it("shows the load error and a no-results state", async () => {
    api.listNotifications.mockRejectedValue(new Error("down"));
    mount(Host);
    await list.load();
    expect(list.loadError.value).toBeTruthy();
    api.listNotifications.mockResolvedValue({ data: [] });
    list.filterUnread.value = true;
    list.onFilterChange();
    await flushPromises();
    expect(list.noResults.value).toBe(true);
  });
});

describe("NotificationPanel", () => {
  it("renders groups, empty and error states", async () => {
    const groups = groupForPanel([n("a", { severity: "critical" }), n("b", { state: "resolved" })]);
    const w = mount(NotificationPanel, { props: { groups, firstLoad: false, refreshing: false, error: null } });
    expect(w.findAll('[data-testid="notification-row"]')).toHaveLength(2);
    expect(w.text()).toContain("notifications.group.critical");
    expect(w.text()).toContain("notifications.group.resolved");
    await w.findAll('[data-testid="notification-row"]')[0]!.trigger("click");
    expect(w.emitted("select")?.[0]?.[0]).toMatchObject({ id: "a" });

    const empty = mount(NotificationPanel, { props: { groups: [], firstLoad: false, refreshing: false, error: null } });
    expect(empty.find('[data-testid="notification-empty"]').exists()).toBe(true);

    const failed = mount(NotificationPanel, { props: { groups: [], firstLoad: false, refreshing: false, error: "Boom" } });
    expect(failed.text()).toContain("Boom");
    await failed.find("button").trigger("click");
    expect(failed.emitted("retry")).toBeTruthy();
  });
});
