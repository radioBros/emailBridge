import { beforeEach, describe, expect, it, vi } from "vitest";
import { useRoutes } from "@/features/routes/composables/useRoutes";
import {
  CEL_MAX_BYTES,
  parseCelErrorPosition,
  tokenizeCel,
} from "@/features/routes/celHighlight";

vi.mock("@/api/routes", () => ({
  listRoutes: vi.fn(),
  createRoute: vi.fn(),
  patchRoute: vi.fn(),
  deleteRoute: vi.fn(),
  testRoute: vi.fn(),
  listRouteVersions: vi.fn(),
  restoreRouteVersion: vi.fn(),
  dryRunRoute: vi.fn(),
  getJob: vi.fn(),
  compileBuilder: vi.fn(),
  expressibleBuilder: vi.fn(),
}));

vi.mock("@/api/webhooks", () => ({
  listWebhookEndpoints: vi.fn(),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : "err"),
    fieldErrors: () => ({}),
  }),
}));

import {
  createRoute,
  deleteRoute,
  dryRunRoute,
  getJob,
  listRouteVersions,
  listRoutes,
  patchRoute,
  restoreRouteVersion,
  testRoute,
} from "@/api/routes";
import { listWebhookEndpoints } from "@/api/webhooks";

const sample = {
  id: "rte_1",
  organizationId: "org_1",
  name: "PEC receipts",
  enabled: true,
  endpointId: "whe_1",
  currentVersion: 1,
  filter: 'type == "message.received"',
  map: '{"id": data.messageId}',
  order: 0,
  createdAt: "2026-10-25T00:00:00Z",
  updatedAt: "2026-10-25T00:00:00Z",
  version: 1,
};

describe("useRoutes", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads routes and endpoint labels", async () => {
    vi.mocked(listRoutes).mockResolvedValue({
      data: [sample],
      nextCursor: null,
      total: 1,
    });
    vi.mocked(listWebhookEndpoints).mockResolvedValue({
      data: [
        {
          id: "whe_1",
          organizationId: "org_1",
          name: "Primary",
          kind: "http",
          enabled: true,
          events: ["*"],
          security: { signatureMode: "standard" },
          retry: { maxAttempts: 10, backoffBase: "5s", backoffJitter: 0.2, timeout: "30s" },
          breaker: { state: "closed", failures: 0 },
          createdAt: "2026-10-25T00:00:00Z",
          updatedAt: "2026-10-25T00:00:00Z",
          version: 1,
        },
      ],
      nextCursor: null,
    });
    const r = useRoutes();
    await r.loadEndpoints();
    await r.load();
    expect(r.rows.value).toHaveLength(1);
    expect(r.endpointLabel("whe_1")).toContain("Primary");
  });

  it("creates, updates, and tests a route", async () => {
    vi.mocked(createRoute).mockResolvedValue(sample);
    vi.mocked(patchRoute).mockResolvedValue({ ...sample, enabled: false });
    vi.mocked(listRoutes).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    vi.mocked(testRoute).mockResolvedValue({
      matched: true,
      mapped: { id: "msg_1" },
      costUsed: 120,
      durationMs: 3,
    });
    const r = useRoutes();
    expect(
      await r.create({
        name: "PEC receipts",
        endpointId: "whe_1",
        filter: sample.filter,
      }),
    ).toBeTruthy();
    expect(await r.setEnabled(sample, false)).toBe(true);
    const result = await r.runTest("rte_1", '{"type":"message.received"}');
    expect(result?.matched).toBe(true);
    expect(result?.mapped).toEqual({ id: "msg_1" });
  });

  it("surfaces invalid JSON on test", async () => {
    const r = useRoutes();
    expect(await r.runTest("rte_1", "{not-json")).toBeNull();
    expect(r.testError.value).toMatch(/json/i);
  });

  it("paginates, updates, deletes and clears test", async () => {
    vi.mocked(listRoutes).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    vi.mocked(patchRoute).mockResolvedValue(sample);
    vi.mocked(deleteRoute).mockResolvedValue(undefined);
    const r = useRoutes();
    await r.setPage(3);
    await r.setIpp(10);
    await r.setSortBy([{ key: "name", order: "desc" }]);
    await r.setSearch("pec");
    expect(listRoutes).toHaveBeenCalledWith(expect.objectContaining({ q: "pec", page: 1 }));
    expect(await r.update("rte_1", { filter: "true" })).toBeTruthy();
    expect(await r.remove("rte_1")).toBe(true);
    r.testResult.value = { matched: false, costUsed: 1, durationMs: 1 };
    r.clearTest();
    expect(r.testResult.value).toBeNull();
  });

  it("surfaces load errors", async () => {
    vi.mocked(listRoutes).mockRejectedValue(new Error("down"));
    const r = useRoutes();
    await r.load();
    expect(r.loadError.value).toBe("down");
    vi.mocked(createRoute).mockRejectedValue(new Error("bad"));
    expect(await r.create({ name: "n", endpointId: "whe_1" })).toBeNull();
    expect(r.formError.value).toBe("bad");
  });

  it("loads versions and restores", async () => {
    vi.mocked(listRouteVersions).mockResolvedValue({
      data: [
        {
          id: "rv_2",
          organizationId: "org_1",
          routeId: "rte_1",
          version: 2,
          filter: "true",
          createdBy: "usr_1",
          createdAt: "2026-10-26T00:00:00Z",
        },
        {
          id: "rv_1",
          organizationId: "org_1",
          routeId: "rte_1",
          version: 1,
          filter: 'type == "message.received"',
          createdBy: "usr_1",
          createdAt: "2026-10-25T00:00:00Z",
        },
      ],
      total: 2,
    });
    vi.mocked(restoreRouteVersion).mockResolvedValue({ ...sample, currentVersion: 3, filter: 'type == "message.received"' });
    vi.mocked(listRoutes).mockResolvedValue({ data: [sample], nextCursor: null, total: 1 });
    const r = useRoutes();
    await r.loadVersions("rte_1");
    expect(r.versions.value).toHaveLength(2);
    expect(await r.restoreVersion("rte_1", 1)).toBeTruthy();
    expect(restoreRouteVersion).toHaveBeenCalledWith("rte_1", 1);
  });

  it("polls dry-run job to success", async () => {
    vi.mocked(dryRunRoute).mockResolvedValue({
      job: {
        id: "job_1",
        organizationId: "org_1",
        kind: "rules.dryRun",
        state: "queued",
        createdAt: "2026-10-26T00:00:00Z",
        updatedAt: "2026-10-26T00:00:00Z",
      },
    } as never);
    vi.mocked(getJob)
      .mockResolvedValueOnce({
        id: "job_1",
        organizationId: "org_1",
        kind: "rules.dryRun",
        state: "running",
        progress: { done: 1, total: 3 },
        createdAt: "2026-10-26T00:00:00Z",
        updatedAt: "2026-10-26T00:00:01Z",
      } as never)
      .mockResolvedValueOnce({
        id: "job_1",
        organizationId: "org_1",
        kind: "rules.dryRun",
        state: "succeeded",
        result: {
          matched: 2,
          scanned: 3,
          samples: [{ eventId: "evt_1", mapped: { ok: true } }],
          errorsByPhase: {},
          cost: { total: 10, max: 5, avg: 3.3 },
          windowHours: 24,
          limit: 1000,
        },
        createdAt: "2026-10-26T00:00:00Z",
        updatedAt: "2026-10-26T00:00:02Z",
      } as never);
    const r = useRoutes();
    await r.runDryRun("rte_1", { windowHours: 24, limit: 100 });
    expect(r.dryRunResult.value?.matched).toBe(2);
    r.clearDryRun();
    expect(r.dryRunJob.value).toBeNull();
  });
});

describe("celHighlight", () => {
  it("tokenises keywords, strings and functions", () => {
    const tokens = tokenizeCel('type == "message.received" && hasHeader("x")');
    const kinds = tokens.map((t) => t.kind);
    expect(kinds).toContain("string");
    expect(kinds).toContain("function");
    expect(kinds).toContain("operator");
  });

  it("tokenises comments, numbers, escapes and punctuation", () => {
    const tokens = tokenizeCel("// note\nsize(list) > 1.5 && true\n{'a': \"b\\\"c\"}");
    expect(tokens.some((t) => t.kind === "comment")).toBe(true);
    expect(tokens.some((t) => t.kind === "number")).toBe(true);
    expect(tokens.some((t) => t.kind === "keyword")).toBe(true);
    expect(tokens.some((t) => t.kind === "punct")).toBe(true);
  });

  it("parses error positions", () => {
    expect(parseCelErrorPosition("syntax error at line 2 column 5")).toMatchObject({
      line: 2,
      column: 5,
    });
    expect(parseCelErrorPosition("oops 3:14")).toMatchObject({ line: 3, column: 14 });
    expect(parseCelErrorPosition("")).toEqual({ message: "" });
    expect(parseCelErrorPosition("plain")).toEqual({ message: "plain" });
  });

  it("exposes the 4 KiB limit", () => {
    expect(CEL_MAX_BYTES).toBe(4096);
  });
});
