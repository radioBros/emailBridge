import { beforeEach, describe, expect, it, vi } from "vitest";
import { useWebhooks } from "@/features/webhooks/composables/useWebhooks";
import {
  WEBHOOK_SECRET_RE,
  defaultDelivery,
  defaultRetry,
  generateWebhookSecret,
} from "@/features/webhooks/eventTypes";

vi.mock("@/api/webhooks", () => ({
  listWebhookEndpoints: vi.fn(),
  createWebhookEndpoint: vi.fn(),
  patchWebhookEndpoint: vi.fn(),
  deleteWebhookEndpoint: vi.fn(),
  rotateWebhookEndpointSecret: vi.fn(),
  testWebhookEndpoint: vi.fn(),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : "err"),
    fieldErrors: () => ({}),
  }),
}));

import {
  createWebhookEndpoint,
  deleteWebhookEndpoint,
  listWebhookEndpoints,
  patchWebhookEndpoint,
  rotateWebhookEndpointSecret,
  testWebhookEndpoint,
} from "@/api/webhooks";

const sample = {
  id: "whe_1",
  organizationId: "org_1",
  name: "Primary",
  kind: "http" as const,
  enabled: true,
  url: "https://hooks.example.test/eb",
  events: ["message.received"],
  security: { signatureMode: "standard" as const },
  retry: defaultRetry(),
  delivery: defaultDelivery(),
  breaker: { state: "closed" as const, failures: 0 },
  createdAt: "2026-10-25T00:00:00Z",
  updatedAt: "2026-10-25T00:00:00Z",
  version: 1,
};

const disabledSample = {
  ...sample,
  id: "whe_2",
  name: "Gone hook",
  enabled: false,
  disabledReason: { code: "http_410", at: "2026-10-26T12:00:00Z" },
};

describe("useWebhooks", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads a page of endpoints", async () => {
    vi.mocked(listWebhookEndpoints).mockResolvedValue({
      data: [sample, disabledSample],
      nextCursor: null,
      total: 2,
    });
    const w = useWebhooks();
    await w.load();
    expect(w.rows.value).toHaveLength(2);
    expect(w.total.value).toBe(2);
    expect(w.rows.value[1]?.disabledReason?.code).toBe("http_410");
    expect(w.isFirstLoad.value).toBe(false);
    expect(listWebhookEndpoints).toHaveBeenCalledWith(
      expect.objectContaining({ page: 1, includeTotal: true }),
    );
  });

  it("creates with delivery options", async () => {
    vi.mocked(createWebhookEndpoint).mockResolvedValue({
      ...sample,
      delivery: { ...defaultDelivery(), ordering: "bestEffort", maxConcurrency: 4 },
    });
    vi.mocked(listWebhookEndpoints).mockResolvedValue({
      data: [sample],
      nextCursor: null,
      total: 1,
    });
    const w = useWebhooks();
    const body = {
      name: "Primary",
      kind: "http" as const,
      events: ["message.received"],
      url: "https://hooks.example.test/eb",
      retry: defaultRetry(),
      delivery: { ...defaultDelivery(), ordering: "bestEffort" as const, maxConcurrency: 4 },
    };
    await w.create(body);
    expect(vi.mocked(createWebhookEndpoint).mock.calls[0]?.[0].delivery).toEqual(body.delivery);
  });

  it("sends q on search and resets page", async () => {
    vi.mocked(listWebhookEndpoints).mockResolvedValue({
      data: [sample],
      nextCursor: null,
      total: 1,
    });
    const w = useWebhooks();
    w.page.value = 3;
    await w.setSearch("verbal");
    expect(w.page.value).toBe(1);
    expect(listWebhookEndpoints).toHaveBeenCalledWith(
      expect.objectContaining({ q: "verbal", page: 1 }),
    );
  });

  it("creates with a generated secret and reveals it", async () => {
    vi.mocked(createWebhookEndpoint).mockResolvedValue(sample);
    vi.mocked(listWebhookEndpoints).mockResolvedValue({
      data: [sample],
      nextCursor: null,
      total: 1,
    });
    const w = useWebhooks();
    const created = await w.create({
      name: "Primary",
      kind: "http",
      events: ["message.received"],
      url: "https://hooks.example.test/eb",
      retry: defaultRetry(),
    });
    expect(created?.id).toBe("whe_1");
    expect(w.revealedSecret.value).toBeTruthy();
    expect(vi.mocked(createWebhookEndpoint).mock.calls[0]?.[0].secret).toBeTruthy();
  });

  it("rotates secret and tests delivery", async () => {
    vi.mocked(rotateWebhookEndpointSecret).mockResolvedValue({
      id: "sec_1",
      secret: "new-secret",
      secretPreview: "…cret",
      expiresAt: null,
    });
    vi.mocked(listWebhookEndpoints).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    vi.mocked(testWebhookEndpoint).mockResolvedValue({
      at: "2026-10-26T00:00:00Z",
      latencyMs: 42,
      statusCode: 200,
      responseExcerpt: "ok",
    });
    const w = useWebhooks();
    const rotated = await w.rotateSecret("whe_1");
    expect(rotated?.secret).toBe("new-secret");
    expect(w.revealedSecret.value).toBe("new-secret");
    const attempt = await w.runTest("whe_1");
    expect(attempt?.statusCode).toBe(200);
  });

  it("deletes and toggles enabled", async () => {
    vi.mocked(deleteWebhookEndpoint).mockResolvedValue(undefined);
    vi.mocked(patchWebhookEndpoint).mockResolvedValue({ ...sample, enabled: false });
    vi.mocked(listWebhookEndpoints).mockResolvedValue({ data: [], nextCursor: null, total: 0 });
    const w = useWebhooks();
    expect(await w.remove("whe_1")).toBe(true);
    expect(await w.setEnabled(sample, false)).toBe(true);
    expect(patchWebhookEndpoint).toHaveBeenCalledWith("whe_1", { enabled: false });
  });

  it("paginates, sorts, updates and clears panels", async () => {
    vi.mocked(listWebhookEndpoints).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    vi.mocked(patchWebhookEndpoint).mockResolvedValue(sample);
    const w = useWebhooks();
    await w.setPage(2);
    await w.setIpp(50);
    await w.setSortBy([{ key: "name", order: "asc" }]);
    await w.setSearch("hook");
    expect(w.search.value).toBe("hook");
    expect(await w.update("whe_1", { name: "Renamed" })).toBeTruthy();
    w.revealedSecret.value = "x";
    w.testResult.value = {
      at: "2026-10-26T00:00:00Z",
      latencyMs: 1,
    };
    w.clearSecret();
    w.clearTest();
    expect(w.revealedSecret.value).toBeNull();
    expect(w.testResult.value).toBeNull();
  });

  it("surfaces load and mutation errors", async () => {
    vi.mocked(listWebhookEndpoints).mockRejectedValue(new Error("down"));
    const w = useWebhooks();
    await w.load();
    expect(w.loadError.value).toBe("down");
    vi.mocked(createWebhookEndpoint).mockRejectedValue(new Error("bad"));
    expect(await w.create({ name: "n", kind: "http", events: ["*"] })).toBeNull();
    expect(w.formError.value).toBe("bad");
    vi.mocked(testWebhookEndpoint).mockRejectedValue(new Error("fail"));
    expect(await w.runTest("whe_1")).toBeNull();
    expect(w.testError.value).toBe("fail");
  });
});

describe("webhook helpers", () => {
  it("generates whsec_ + std base64 secrets", () => {
    const a = generateWebhookSecret();
    const b = generateWebhookSecret();
    expect(a).toMatch(WEBHOOK_SECRET_RE);
    expect(a.startsWith("whsec_")).toBe(true);
    expect(a).not.toBe(b);
    const payload = a.slice("whsec_".length);
    expect(() => atob(payload)).not.toThrow();
    expect(atob(payload).length).toBe(32);
  });
});
