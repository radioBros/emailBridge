import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiProblemError } from "@/api/client";

vi.mock("@/api/client", async (orig) => {
  const real = await orig<typeof import("@/api/client")>();
  return {
    ...real,
    api: {
      get: vi.fn(),
      patch: vi.fn(),
      post: vi.fn(),
      delete: vi.fn(),
    },
  };
});

import { api } from "@/api/client";
import { getKpis } from "@/api/dashboard";

describe("getKpis", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
  });

  it("returns the kpis object when present", async () => {
    const kpis = {
      period: "24h",
      from: "2026-10-27T00:00:00Z",
      to: "2026-10-28T00:00:00Z",
      bucketSeconds: 3600,
      generatedAt: "2026-10-28T00:00:00Z",
      accounts: { total: 1, connected: 1, attention: 0 },
      messagesReceived: { current: 0, previous: 0, buckets: [] },
      messagesSent: { current: 0, previous: 0, buckets: [] },
      messagesFailed: { current: 0, previous: 0, buckets: [] },
      webhooksDelivered: { current: 0, previous: 0, buckets: [] },
      webhooksFailed: { current: 0, previous: 0, buckets: [] },
    };
    vi.mocked(api.get).mockResolvedValueOnce({
      accounts: {},
      outboundMessages: {},
      deliveriesLast24h: {},
      eventsLast24h: 0,
      deadLetters: 0,
      kpis,
    });
    await expect(getKpis({ period: "24h" })).resolves.toEqual(kpis);
    expect(api.get).toHaveBeenCalledWith("/stats?period=24h");
  });

  it("throws ApiProblemError when kpis are missing (would show overview error)", async () => {
    vi.mocked(api.get).mockResolvedValueOnce({
      accounts: { connected: 1 },
      outboundMessages: {},
      deliveriesLast24h: {},
      eventsLast24h: 0,
      deadLetters: 0,
    });
    let caught: unknown;
    try {
      await getKpis({ period: "24h" });
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(ApiProblemError);
    expect((caught as ApiProblemError).problem.code).toBe("kpis_missing");
    expect(api.get).toHaveBeenCalledWith("/stats?period=24h");
  });

  it("sends from / to instead of period for a custom window", async () => {
    vi.mocked(api.get).mockResolvedValueOnce({ kpis: { period: "custom" } });
    await getKpis({ period: "custom", from: "2026-10-01T00:00:00.000Z", to: "2026-10-15T23:59:59.999Z" });
    const url = String(vi.mocked(api.get).mock.calls.at(-1)?.[0]);
    const params = new URLSearchParams(url.split("?")[1]);
    expect(params.get("from")).toBe("2026-10-01T00:00:00.000Z");
    expect(params.get("to")).toBe("2026-10-15T23:59:59.999Z");
    expect(params.has("period")).toBe(false);
  });
});
