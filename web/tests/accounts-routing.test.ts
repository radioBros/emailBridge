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
        id: "evt_latest",
        type: "message.received",
        source: "/orgs/o1",
        subject: "s",
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
}));

vi.mock("@/api/routes", () => ({
  listRoutes: vi.fn(async () => ({
    data: [
      {
        id: "rte_1",
        organizationId: "org_1",
        name: "All mail",
        endpointId: "ep_1",
        filter: "true",
        enabled: true,
        order: 10,
        currentVersion: 1,
        version: 1,
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
      },
    ],
    total: 1,
  })),
  testRoute: vi.fn(async () => ({
    matched: true,
    costUsed: 1,
    durationMs: 5,
    mapped: { hello: "world" },
  })),
}));

vi.mock("@/api/webhooks", () => ({
  listWebhookEndpoints: vi.fn(async () => ({
    data: [
      {
        id: "ep_1",
        organizationId: "org_1",
        name: "CRM",
        kind: "http",
        url: "https://example.test/hook",
        enabled: true,
        events: null,
        breaker: { state: "closed" },
        delivery: { ordering: "bestEffort", maxConcurrency: 1, contentType: "json", retryOn4xx: false },
        retry: {},
        security: {},
        createdAt: "2026-01-01T00:00:00Z",
        updatedAt: "2026-01-01T00:00:00Z",
        version: 1,
      },
    ],
    total: 1,
  })),
}));

import { listEvents } from "@/api/events";
import { listRoutes, testRoute } from "@/api/routes";
import { listWebhookEndpoints } from "@/api/webhooks";
import { useAccountRouting } from "@/features/accounts/composables/useAccountRouting";

describe("useAccountRouting", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads routes and endpoints", async () => {
    const { load, routes, endpoints, rows, empty } = useAccountRouting(ref("acc_1"));
    await load();
    expect(listRoutes).toHaveBeenCalled();
    expect(listWebhookEndpoints).toHaveBeenCalled();
    expect(routes.value).toHaveLength(1);
    expect(endpoints.value).toHaveLength(1);
    expect(rows.value[0]?.endpointName).toBe("CRM");
    expect(empty.value).toBe(false);
  });

  it("tests every route against the latest account event", async () => {
    const api = useAccountRouting(ref("acc_1"));
    await api.load();
    await api.testWithLatest();
    expect(listEvents).toHaveBeenCalledWith(
      expect.objectContaining({ accountId: "acc_1", limit: 1, sort: "-time" }),
    );
    expect(testRoute).toHaveBeenCalledWith("rte_1", { eventId: "evt_latest" });
    expect(api.latestEvent.value?.id).toBe("evt_latest");
    expect(api.rows.value[0]?.result?.matched).toBe(true);
    expect(api.rows.value[0]?.result?.mapped).toEqual({ hello: "world" });
  });

  it("surfaces no_event when the account has no events", async () => {
    vi.mocked(listEvents).mockResolvedValueOnce({ data: [], total: 0 });
    const api = useAccountRouting(ref("acc_1"));
    await api.load();
    await api.testWithLatest();
    expect(api.testError.value).toBe("no_event");
    expect(testRoute).not.toHaveBeenCalled();
  });
});
