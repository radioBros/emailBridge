import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn(async () => ({ data: [], total: 0 }));

vi.mock("@/api/client", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
  },
}));

describe("events api", () => {
  beforeEach(() => {
    get.mockClear();
  });

  it("listEvents and getEvent build paths", async () => {
    const { listEvents, getEvent, eventsStreamPath } = await import("@/api/events");
    await listEvents({ page: 2, limit: 10, type: "message.*", accountId: "acc_1" });
    expect(get).toHaveBeenCalledWith(expect.stringContaining("/events?"));
    expect(get.mock.calls[0]![0]).toContain("type=message.*");
    await getEvent("evt_1");
    expect(get).toHaveBeenCalledWith("/events/evt_1");
    expect(eventsStreamPath({ type: "a", accountId: "b" })).toContain("type=a");
  });
});
