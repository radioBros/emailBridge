import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

import { api } from "@/api/client";
import {
  compileBuilder,
  createRoute,
  deleteRoute,
  dryRunRoute,
  expressibleBuilder,
  getJob,
  getRoute,
  listRouteVersions,
  listRoutes,
  patchRoute,
  restoreRouteVersion,
  testRoute,
} from "@/api/routes";

describe("routes api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists with query params", async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [], nextCursor: null });
    await listRoutes({ page: 1, limit: 25, sort: "order", cursor: "abc" });
    expect(api.get).toHaveBeenCalledWith(expect.stringContaining("/routes?"));
    expect(api.get).toHaveBeenCalledWith(expect.stringContaining("cursor=abc"));
  });

  it("CRUD + test paths", async () => {
    vi.mocked(api.get).mockResolvedValue({ id: "rte_1" });
    vi.mocked(api.post).mockResolvedValue({ id: "rte_1" });
    vi.mocked(api.patch).mockResolvedValue({ id: "rte_1" });
    vi.mocked(api.delete).mockResolvedValue(undefined);
    await getRoute("rte_1");
    await createRoute({ name: "r", endpointId: "whe_1" });
    await patchRoute("rte_1", { enabled: false });
    await deleteRoute("rte_1");
    await testRoute("rte_1", { event: { type: "message.received" } });
    expect(api.get).toHaveBeenCalledWith("/routes/rte_1");
    expect(api.post).toHaveBeenCalledWith("/routes/rte_1/test", {
      event: { type: "message.received" },
    });
    expect(api.delete).toHaveBeenCalledWith("/routes/rte_1");
  });

  it("builder versions dry-run and jobs", async () => {
    vi.mocked(api.post).mockResolvedValue({ cel: "true", ok: true });
    vi.mocked(api.get).mockResolvedValue({ data: [], total: 0 });
    await compileBuilder({ match: "all", conditions: [] });
    expect(api.post).toHaveBeenCalledWith("/routes/builder/compile", {
      builder: { match: "all", conditions: [] },
    });
    await expressibleBuilder("true");
    expect(api.post).toHaveBeenCalledWith("/routes/builder/expressible", { cel: "true" });
    await listRouteVersions("rte_1", { page: 1, limit: 20, includeTotal: true });
    expect(api.get).toHaveBeenCalledWith(
      expect.stringContaining("/routes/rte_1/versions?"),
    );
    await restoreRouteVersion("rte_1", 2);
    expect(api.post).toHaveBeenCalledWith("/routes/rte_1/versions/2/restore", {});
    await dryRunRoute("rte_1", { windowHours: 12, limit: 100 });
    expect(api.post).toHaveBeenCalledWith("/routes/rte_1/dry-run", {
      windowHours: 12,
      limit: 100,
    });
    await getJob("job_1");
    expect(api.get).toHaveBeenCalledWith("/jobs/job_1");
  });
});
