import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { formatPartitions, formatUptime } from "@/features/cluster/helpers/format";
import { queueLabelKey, roleColor } from "@/features/cluster/helpers/roles";

vi.mock("@/api/cluster", () => ({
  getCluster: vi.fn(),
  listQueues: vi.fn(),
  drainNode: vi.fn(),
  patchQueue: vi.fn(),
}));

vi.mock("@/api/problems", () => ({
  problemOf: (err: unknown) =>
    err && typeof err === "object" && "problem" in (err as object)
      ? (err as { problem: { detail: string; status?: number } }).problem
      : null,
}));

vi.mock("@/stores/session", () => ({
  useSessionStore: () => ({
    profile: { instanceRole: "owner" },
  }),
}));

vi.mock("@/stores/notify", () => ({
  useNotifyStore: () => ({ push: vi.fn() }),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k }),
}));

describe("cluster helpers", () => {
  it("formats partitions and uptime", () => {
    expect(formatPartitions({ events: 2, delivery: 1 })).toBe(
      "events 2 · delivery 1",
    );
    expect(formatPartitions({})).toBe("—");
    expect(formatPartitions(undefined)).toBe("—");

    const started = new Date(Date.now() - 90 * 60 * 1000).toISOString();
    expect(formatUptime(started)).toMatch(/1h/);
    expect(formatUptime(null)).toBe("—");
  });

  it("maps roles and queue label keys", () => {
    expect(roleColor("api")).toBe("primary");
    expect(roleColor("sync")).toBe("info");
    expect(roleColor("weird")).toBe("grey");
    expect(queueLabelKey("EMAILBRIDGE_SUBMIT")).toBe("submit");
    expect(queueLabelKey("EMAILBRIDGE_SYSMAIL")).toBe("sysmail");
  });
});

describe("useCluster", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("loads cluster and queues for an owner", async () => {
    const api = await import("@/api/cluster");
    vi.mocked(api.getCluster).mockResolvedValue({
      nodes: [
        {
          id: "api-1",
          roles: ["api", "scheduler"],
          version: "0.1.0",
          startedAt: new Date(Date.now() - 3600_000).toISOString(),
          seenAt: new Date().toISOString(),
          draining: false,
          accounts: 3,
          partitions: { events: 1 },
        },
      ],
      totals: { accounts: 3, partitions: { events: 1 } },
      leader: "api-1",
      nodeCount: 1,
    });
    vi.mocked(api.listQueues).mockResolvedValue([
      {
        name: "EMAILBRIDGE_SUBMIT",
        pending: 2,
        ackPending: 0,
        consumers: 1,
        paused: false,
        keep: "7d",
      },
    ]);

    const { useCluster } = await import(
      "@/features/cluster/composables/useCluster"
    );
    const c = useCluster();
    await c.load();

    expect(c.nodes.value).toHaveLength(1);
    expect(c.leader.value).toBe("api-1");
    expect(c.queues.value[0]?.name).toBe("EMAILBRIDGE_SUBMIT");
    expect(c.forbidden.value).toBe(false);
  });

  it("marks forbidden on 403", async () => {
    const { ApiProblemError } = await import("@/api/client");
    const api = await import("@/api/cluster");
    vi.mocked(api.getCluster).mockRejectedValue(
      new ApiProblemError({
        status: 403,
        code: "forbidden",
        title: "denied",
        detail: "denied",
      }),
    );
    vi.mocked(api.listQueues).mockRejectedValue(
      new ApiProblemError({
        status: 403,
        code: "forbidden",
        title: "denied",
        detail: "denied",
      }),
    );

    const { useCluster } = await import(
      "@/features/cluster/composables/useCluster"
    );
    const c = useCluster();
    await c.load();
    expect(c.forbidden.value).toBe(true);
  });

  it("drains a node and patches queue pause/keep", async () => {
    const api = await import("@/api/cluster");
    vi.mocked(api.getCluster).mockResolvedValue({
      nodes: [],
      totals: { accounts: 0 },
      leader: "",
      nodeCount: 0,
    });
    vi.mocked(api.listQueues).mockResolvedValue([
      {
        name: "EMAILBRIDGE_EVENTS",
        pending: 0,
        ackPending: 0,
        consumers: 1,
        paused: false,
        keep: "7d",
      },
    ]);
    vi.mocked(api.drainNode).mockResolvedValue(undefined);
    vi.mocked(api.patchQueue).mockImplementation(async (name, body) => ({
      name,
      pending: 0,
      ackPending: 0,
      consumers: 1,
      paused: Boolean(body.paused),
      keep: body.keep ?? "7d",
    }));

    const { useCluster } = await import(
      "@/features/cluster/composables/useCluster"
    );
    const c = useCluster();
    await c.load();

    await c.drain({
      id: "sync-1",
      roles: ["sync"],
      version: "0.1.0",
      startedAt: new Date().toISOString(),
      seenAt: new Date().toISOString(),
      draining: false,
      accounts: 0,
    });
    expect(api.drainNode).toHaveBeenCalledWith("sync-1");

    const q = c.queues.value[0]!;
    await c.setPaused(q, true);
    expect(api.patchQueue).toHaveBeenCalledWith("EMAILBRIDGE_EVENTS", {
      paused: true,
    });
    await c.setKeep(q, "3d");
    expect(api.patchQueue).toHaveBeenCalledWith("EMAILBRIDGE_EVENTS", {
      keep: "3d",
    });
  });

  it("silent refresh does not set table loading flags", async () => {
    const api = await import("@/api/cluster");
    vi.mocked(api.getCluster).mockResolvedValue({
      nodes: [],
      totals: { accounts: 0 },
      leader: "",
      nodeCount: 0,
    });
    vi.mocked(api.listQueues).mockResolvedValue([]);

    const { useCluster } = await import(
      "@/features/cluster/composables/useCluster"
    );
    const c = useCluster();
    await c.load();
    expect(c.tableLoading.value).toBe(false);

    let resolveCluster!: (v: unknown) => void;
    vi.mocked(api.getCluster).mockReturnValue(
      new Promise((resolve) => {
        resolveCluster = resolve;
      }) as never,
    );
    const pending = c.load(true);
    expect(c.tableLoading.value).toBe(false);
    expect(c.queuesTableLoading.value).toBe(false);
    resolveCluster({
      nodes: [],
      totals: { accounts: 0 },
      leader: "",
      nodeCount: 0,
    });
    await pending;
  });

  it("sorts and pages the node snapshot locally", async () => {
    const api = await import("@/api/cluster");
    vi.mocked(api.getCluster).mockResolvedValue({
      nodes: [
        {
          id: "z-node",
          roles: ["sync"],
          version: "0.1.0",
          startedAt: "2026-01-01T00:00:00Z",
          seenAt: "2026-10-26T10:00:00Z",
          draining: false,
          accounts: 1,
        },
        {
          id: "a-node",
          roles: ["api"],
          version: "0.2.0",
          startedAt: "2026-01-01T00:00:00Z",
          seenAt: "2026-10-26T12:00:00Z",
          draining: false,
          accounts: 9,
        },
      ],
      totals: { accounts: 10 },
      leader: "a-node",
      nodeCount: 2,
    });
    vi.mocked(api.listQueues).mockResolvedValue([]);

    const { useCluster } = await import(
      "@/features/cluster/composables/useCluster"
    );
    const c = useCluster();
    await c.load();

    c.ipp.value = 1;
    c.sortBy.value = [{ key: "id", order: "asc" }];
    expect(c.nodesTotal.value).toBe(2);
    expect(c.nodes.value.map((n) => n.id)).toEqual(["a-node"]);
    c.page.value = 2;
    expect(c.nodes.value.map((n) => n.id)).toEqual(["z-node"]);
  });
});
