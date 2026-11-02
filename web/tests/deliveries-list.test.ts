import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({
    t: (k: string, p?: Record<string, unknown>) => (p ? `${k}:${JSON.stringify(p)}` : k),
    te: () => true,
  }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => String(err),
    fieldErrors: () => ({}),
  }),
}));

vi.mock("@/api/deliveries", () => ({
  listDeliveries: vi.fn(async () => ({
    data: [
      {
        id: "dlv_1",
        organizationId: "org_1",
        endpointId: "whe_1",
        eventId: "evt_1",
        eventType: "message.received",
        state: "failed",
        createdAt: "2026-10-26T10:00:00Z",
        expireAt: "2026-11-26T10:00:00Z",
        attempts: [{ at: "2026-10-26T10:01:00Z", statusCode: 500, latencyMs: 12 }],
        accountId: "acc_1",
      },
    ],
    nextCursor: null,
    total: 1,
  })),
  getDelivery: vi.fn(),
  replayDelivery: vi.fn(async () => ({ id: "dlv_2" })),
  bulkReplayDeliveries: vi.fn(async () => ({
    job: { id: "job_1", kind: "deliveries.replay", state: "queued", createdAt: "2026-10-26T10:00:00Z" },
  })),
  getJob: vi.fn(async () => ({
    id: "job_1",
    kind: "deliveries.replay",
    state: "succeeded",
    createdAt: "2026-10-26T10:00:00Z",
    progress: { done: 1, total: 1 },
  })),
}));

vi.mock("@/api/webhooks", () => ({
  listWebhookEndpoints: vi.fn(async () => ({ data: [{ id: "whe_1", name: "Hook" }], nextCursor: null })),
}));

vi.mock("@/api/routes", () => ({
  listRoutes: vi.fn(async () => ({ data: [], nextCursor: null })),
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

import { listDeliveries, bulkReplayDeliveries } from "@/api/deliveries";
import { useDeliveriesList } from "@/features/deliveries/composables/useDeliveriesList";
import { FAILED_QUICK, lastStatusCode } from "@/features/deliveries/helpers/states";
import { jobProgressText } from "@/features/deliveries/helpers/jobPoll";

describe("useDeliveriesList", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("loads delivery rows", async () => {
    const { items, total, load, isFirstLoad, empty } = useDeliveriesList();
    const pending = load();
    expect(isFirstLoad.value).toBe(true);
    await pending;
    expect(listDeliveries).toHaveBeenCalled();
    expect(items.value).toHaveLength(1);
    expect(total.value).toBe(1);
    expect(empty.value).toBe(false);
    expect(lastStatusCode(items.value[0]!.attempts)).toBe(500);
  });

  it("applies failed quick filter and bulk replays selected", async () => {
    const list = useDeliveriesList();
    await list.load();
    list.setFailedQuick(true);
    expect(list.filterState.value).toBe(FAILED_QUICK);
    await Promise.resolve();
    list.selected.value = ["dlv_1"];
    await list.replaySelected();
    expect(bulkReplayDeliveries).toHaveBeenCalledWith({ ids: ["dlv_1"] });
  });

  it("formats job progress", () => {
    expect(
      jobProgressText({
        id: "job_1",
        kind: "x",
        state: "running",
        createdAt: "2026-10-26T10:00:00Z",
        progress: { done: 2, total: 5 },
      }),
    ).toBe("2/5");
  });
});
