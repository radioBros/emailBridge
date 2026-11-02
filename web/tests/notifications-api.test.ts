import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn();
const post = vi.fn();
const patch = vi.fn();
const rawGet = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: (...a: unknown[]) => get(...a),
    post: (...a: unknown[]) => post(...a),
    patch: (...a: unknown[]) => patch(...a),
    raw: { get: (...a: unknown[]) => rawGet(...a) },
  },
}));

import {
  getNotificationPrefs,
  getNotificationsSummary,
  listNotifications,
  markAllNotificationsRead,
  markNotificationsRead,
  patchNotificationPrefs,
  resolveNotification,
} from "@/api/notifications";

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  patch.mockReset();
  rawGet.mockReset();
});

describe("notifications api", () => {
  it("drops empty filters from the list query", async () => {
    rawGet.mockResolvedValue({ data: { data: [], total: 0 } });
    const res = await listNotifications({ state: "open", category: "", severity: undefined, unread: true, limit: 5 });
    expect(rawGet).toHaveBeenCalledWith("/notifications", { params: { state: "open", unread: true, limit: 5 } });
    expect(res.total).toBe(0);
  });

  it("calls summary, read-all and resolve", async () => {
    get.mockResolvedValue({ unread: 1 });
    await getNotificationsSummary();
    expect(get).toHaveBeenCalledWith("/notifications/summary");
    await markAllNotificationsRead();
    expect(post).toHaveBeenCalledWith("/notifications/read-all", {});
    await resolveNotification("ntf_1");
    expect(post).toHaveBeenCalledWith("/notifications/ntf_1/resolve", {});
  });

  it("chunks mark-read at 200 ids", async () => {
    const ids = Array.from({ length: 450 }, (_, i) => `ntf_${i}`);
    await markNotificationsRead(ids);
    expect(post).toHaveBeenCalledTimes(3);
    expect((post.mock.calls[2]![1] as { ids: string[] }).ids).toHaveLength(50);
  });

  it("reads and patches preferences under notifications", async () => {
    get.mockResolvedValue({ settings: { notifications: { mutedCategories: ["queues"] } } });
    expect(await getNotificationPrefs()).toEqual({ mutedCategories: ["queues"] });
    get.mockResolvedValue({});
    expect(await getNotificationPrefs()).toEqual({});
    patch.mockResolvedValue({ settings: {} });
    expect(await patchNotificationPrefs({ quietHours: null })).toEqual({});
    expect(patch).toHaveBeenCalledWith("/me/settings", { notifications: { quietHours: null } });
  });
});
