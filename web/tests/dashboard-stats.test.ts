import { createPinia, setActivePinia } from "pinia";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDashboardStats } from "@/features/home/composables/useDashboardStats";
import { useSessionStore } from "@/stores/session";

vi.mock("@/api/dashboard", () => ({
  getStats: vi.fn(async () => ({
    accounts: {
      connected: 5,
      syncing: 0,
      connecting: 0,
      new: 1,
      connectError: 1,
      authError: 2,
      disabled: 1,
      disconnected: 0,
      deleting: 0,
    },
    outboundMessages: {
      queued: 3,
      failed: 2,
      sent: 10,
      sending: 1,
    },
    deliveriesLast24h: {
      succeeded: 20,
      failed: 2,
      retrying: 1,
    },
    eventsLast24h: 42,
    deadLetters: 1,
  })),
  countSandboxSince: vi.fn(async () => 7),
  countWebhookEndpoints: vi.fn(async () => 4),
  countMembers: vi.fn(async () => 9),
}));

vi.mock("@/api/logs", () => ({
  getLogStats: vi.fn(async () => ({
    from: "2026-01-01T00:00:00Z",
    to: "2026-01-02T00:00:00Z",
    byLevel: { error: 4, info: 20, warn: 3 },
    byCategory: { api: 10 },
  })),
}));

vi.mock("@/api/cluster", () => ({
  listQueues: vi.fn(async () => [
    { name: "outbound", pending: 12, ackPending: 0, consumers: 1, paused: false },
    { name: "webhooks", pending: 0, ackPending: 0, consumers: 1, paused: false },
  ]),
}));

vi.mock("@/api/problems", () => ({
  problemOf: (err: unknown) =>
    err && typeof err === "object" && "problem" in (err as object)
      ? (err as { problem: { detail: string } }).problem
      : { detail: String(err) },
}));

vi.mock("@/api/auth", () => ({
  auth: {
    useSession: () => ({ value: { data: null, isPending: false } }),
    signout: vi.fn(),
  },
}));

const seedMe = (opts: {
  role?: string;
  instanceRole?: string;
  permissions?: string[];
}): void => {
  const session = useSessionStore();
  session.me = {
    kind: "user",
    mfa: true,
    permissions: opts.permissions ?? [],
    memberships: [],
    role: opts.role ?? "viewer",
    user: {
      id: "u1",
      email: "a@b.c",
      name: "Ada",
      locale: "en",
      instanceRole: opts.instanceRole ?? "member",
      status: "active",
    },
  };
};

describe("useDashboardStats", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    seedMe({
      role: "owner",
      instanceRole: "owner",
      permissions: ["logs.read", "instance.admin"],
    });
  });

  it("builds KPI hierarchy and state segments from /v1/stats", async () => {
    const {
      connected,
      accountsTotal,
      attention,
      queued,
      failed,
      stateSegments,
      outboundSlices,
      deliverySlices,
      logLevelSlices,
      queueSlices,
      load,
    } = useDashboardStats();
    await load();

    expect(connected.value.value).toBe(5);
    expect(accountsTotal.value).toBe(10);
    expect(attention.value.value).toBe(4); // 2+1+1
    expect(queued.value.value).toBe(3);
    expect(failed.value.value).toBe(2);

    const connectedSeg = stateSegments.value.find((s) => s.state === "connected");
    expect(connectedSeg?.value).toBe(5);
    expect(connectedSeg?.href).toContain("state=connected");

    const zeroSeg = stateSegments.value.find((s) => s.state === "syncing");
    expect(zeroSeg?.value).toBe(0);

    expect(outboundSlices.value.some((s) => s.key === "queued" && s.value === 3)).toBe(
      true,
    );
    expect(
      deliverySlices.value.some((s) => s.key === "succeeded" && s.value === 20),
    ).toBe(true);
    expect(logLevelSlices.value.some((s) => s.key === "error" && s.value === 4)).toBe(
      true,
    );
    expect(queueSlices.value).toEqual([
      expect.objectContaining({ key: "outbound", value: 12 }),
    ]);
  });

  it("keeps compact metrics when list counts fail after stats ok", async () => {
    const dashboard = await import("@/api/dashboard");
    vi.mocked(dashboard.countMembers).mockRejectedValueOnce(new Error("boom"));

    const { members, webhooks, connected, load } = useDashboardStats();
    await load();

    expect(connected.value.value).toBe(5);
    expect(members.value.error).toBeTruthy();
    expect(webhooks.value.value).toBe(4);
  });

  it("marks account KPIs failed when stats errors", async () => {
    const dashboard = await import("@/api/dashboard");
    vi.mocked(dashboard.getStats).mockRejectedValueOnce(new Error("stats down"));

    const { connected, outboundSlices, members, load } = useDashboardStats();
    await load();

    expect(connected.value.error).toBeTruthy();
    expect(outboundSlices.value).toEqual([]);
    expect(members.value.value).toBe(9);
  });

  it("skips log stats and queues for a viewer (no 403-prone fetches)", async () => {
    seedMe({ role: "viewer", instanceRole: "member", permissions: ["read"] });
    const logs = await import("@/api/logs");
    const cluster = await import("@/api/cluster");

    const { canReadLogLevels, canReadQueues, logLevelSlices, queueSlices, load } =
      useDashboardStats();
    await load();

    expect(canReadLogLevels.value).toBe(false);
    expect(canReadQueues.value).toBe(false);
    expect(logs.getLogStats).not.toHaveBeenCalled();
    expect(cluster.listQueues).not.toHaveBeenCalled();
    expect(logLevelSlices.value).toEqual([]);
    expect(queueSlices.value).toEqual([]);
  });

  it("fetches log levels for operator but not queues", async () => {
    seedMe({
      role: "operator",
      instanceRole: "member",
      permissions: ["read", "logs.read"],
    });
    const logs = await import("@/api/logs");
    const cluster = await import("@/api/cluster");

    const { canReadLogLevels, canReadQueues, logLevelSlices, queueSlices, load } =
      useDashboardStats();
    await load();

    expect(canReadLogLevels.value).toBe(true);
    expect(canReadQueues.value).toBe(false);
    expect(logs.getLogStats).toHaveBeenCalled();
    expect(cluster.listQueues).not.toHaveBeenCalled();
    expect(logLevelSlices.value.some((s) => s.key === "error")).toBe(true);
    expect(queueSlices.value).toEqual([]);
  });

  it("fetches queues only for instance owner", async () => {
    seedMe({
      role: "admin",
      instanceRole: "owner",
      permissions: ["logs.read", "instance.admin"],
    });
    const cluster = await import("@/api/cluster");

    const { canReadQueues, queueSlices, load } = useDashboardStats();
    await load();

    expect(canReadQueues.value).toBe(true);
    expect(cluster.listQueues).toHaveBeenCalled();
    expect(queueSlices.value).toEqual([
      expect.objectContaining({ key: "outbound", value: 12 }),
    ]);
  });
});
