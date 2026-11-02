import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({
    t: (k: string) => k,
    te: () => true,
  }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => String(err),
    fieldErrors: () => ({}),
  }),
}));

vi.mock("@/api/events", () => ({
  listEvents: vi.fn(async () => ({
    data: [
      {
        specversion: "1.0",
        id: "evt_1",
        type: "message.received",
        source: "/organizations/org_1/accounts/acc_1",
        subject: "msg_1",
        time: "2026-10-26T10:00:00Z",
        datacontenttype: "application/json",
        dataschema: "https://emailbridge.dev/schemas/events/message.received/v1.json",
        eborg: "org_1",
        ebaccount: "acc_1",
        ebschema: 1,
        data: { subject: "hi" },
      },
    ],
    nextCursor: null,
    total: 1,
  })),
  getEvent: vi.fn(async (id: string) => ({
    specversion: "1.0",
    id,
    type: "message.received",
    source: "/organizations/org_1",
    subject: "msg_1",
    time: "2026-10-26T10:00:00Z",
    datacontenttype: "application/json",
    dataschema: "https://x",
    eborg: "org_1",
    ebaccount: "acc_1",
    ebschema: 1,
    data: {},
  })),
  eventsStreamPath: vi.fn(() => "/v1/events/stream"),
}));

vi.mock("@/api/outbox", () => ({
  searchAccounts: vi.fn(async () => [
    {
      id: "acc_1",
      name: "Support",
      email: "a@test",
      organizationId: "org_1",
      state: "connected",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
      version: 1,
      imap: {},
      smtp: {},
      sync: {},
    },
  ]),
}));

describe("useEventsList", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("loads events and accounts", async () => {
    const { useEventsList } = await import("@/features/events/composables/useEventsList");
    const list = useEventsList();
    await list.load();
    expect(list.items.value).toHaveLength(1);
    expect(list.total.value).toBe(1);
    await list.loadAccounts("sup");
    expect(list.accountOptions.value[0]?.id).toBe("acc_1");
    list.filterType.value = "message.*";
    list.filterAccountId.value = "acc_1";
    list.filterFrom.value = "2026-01-01T00:00:00Z";
    list.filterTo.value = "2026-12-01T00:00:00Z";
    expect(list.activeFilterCount.value).toBe(3);
    list.clearFilters();
    expect(list.filterType.value).toBe("");
    expect(list.empty.value).toBe(false);
  });

  it("toggleLive starts and stops the stream", async () => {
    const { useEventsList } = await import("@/features/events/composables/useEventsList");
    const list = useEventsList();
    await list.load();
    const start = vi.spyOn(list.stream, "start").mockResolvedValue();
    const stop = vi.spyOn(list.stream, "stop").mockImplementation(() => {
      list.stream.live.value = false;
    });
    await list.toggleLive();
    expect(start).toHaveBeenCalled();
    list.stream.live.value = true;
    await list.toggleLive();
    expect(stop).toHaveBeenCalled();
  });
});

describe("useEventDetail", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("loads one envelope", async () => {
    const { useEventDetail } = await import("@/features/events/composables/useEventDetail");
    const d = useEventDetail();
    await d.show("evt_1");
    expect(d.detail.value?.id).toBe("evt_1");
    expect(d.jsonText.value).toContain("evt_1");
  });
});
