import { beforeEach, describe, expect, it, vi } from "vitest";

const { replayEventsToEndpoint, getJob } = vi.hoisted(() => ({
  replayEventsToEndpoint: vi.fn(async () => ({
    job: { id: "job_1", kind: "events.replay", state: "queued" },
  })),
  getJob: vi.fn(),
}));

vi.mock("@/api/events", () => ({ replayEventsToEndpoint }));
vi.mock("@/api/deliveries", () => ({ getJob }));

describe("replayEvents helpers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("startEventsReplay polls until succeeded", async () => {
    getJob
      .mockResolvedValueOnce({
        id: "job_1",
        kind: "events.replay",
        state: "running",
        progress: { done: 1, total: 2 },
      })
      .mockResolvedValueOnce({
        id: "job_1",
        kind: "events.replay",
        state: "succeeded",
        progress: { done: 2, total: 2 },
      });

    const { startEventsReplay, jobProgressText } = await import(
      "@/features/events/helpers/replayEvents"
    );
    expect(jobProgressText({ state: "queued" } as never)).toBe("queued");
    expect(
      jobProgressText({ state: "running", progress: { done: 1, total: 2 } } as never),
    ).toBe("1/2");

    const updates: string[] = [];
    const job = await startEventsReplay(
      {
        endpointId: "whe_1",
        from: "2026-10-20T00:00:00.000Z",
        to: "2026-10-26T23:59:59.999Z",
      },
      (j) => updates.push(j.state),
    );
    expect(replayEventsToEndpoint).toHaveBeenCalledWith({
      endpointId: "whe_1",
      from: "2026-10-20T00:00:00.000Z",
      to: "2026-10-26T23:59:59.999Z",
    });
    expect(job.state).toBe("succeeded");
    expect(updates).toEqual(["running", "succeeded"]);
  });
});
