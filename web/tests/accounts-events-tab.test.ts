import { beforeEach, describe, expect, it, vi } from "vitest";
import { ref } from "vue";

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

vi.mock("@/api/events", () => ({
  listEvents: vi.fn(async () => ({
    data: [
      {
        specversion: "1.0",
        id: "evt_1",
        type: "message.received",
        source: "/orgs/o1",
        subject: "hello",
        time: "2026-10-26T12:00:00Z",
        datacontenttype: "application/json",
        dataschema: "",
        eborg: "org_1",
        ebaccount: "acc_1",
        ebschema: 1,
        data: {},
      },
    ],
    total: 1,
  })),
  getEvent: vi.fn(),
}));

import { listEvents } from "@/api/events";
import { useAccountRecentEvents } from "@/features/accounts/composables/useAccountRecentEvents";

describe("useAccountRecentEvents", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists events for the account with default sort", async () => {
    const { load, items, total } = useAccountRecentEvents(ref("acc_1"));
    await load();
    expect(listEvents).toHaveBeenCalledWith(
      expect.objectContaining({
        accountId: "acc_1",
        page: 1,
        sort: "-time",
      }),
    );
    expect(items.value).toHaveLength(1);
    expect(total.value).toBe(1);
  });

  it("passes type filter and sort direction", async () => {
    const api = useAccountRecentEvents(ref("acc_1"));
    api.filterType.value = "message.received";
    api.sortBy.value = [{ key: "time", order: "asc" }];
    api.page.value = 2;
    await api.load();
    expect(listEvents).toHaveBeenCalledWith(
      expect.objectContaining({
        accountId: "acc_1",
        type: "message.received",
        sort: "time",
        page: 2,
      }),
    );
  });
});
