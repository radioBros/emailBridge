import { beforeEach, describe, expect, it, vi } from "vitest";
import { ref } from "vue";

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

const getAccountHealth = vi.fn();
const checkAccountHealth = vi.fn();
const listAccountHealth = vi.fn();

vi.mock("@/api/health", () => ({
  getAccountHealth: (...a: unknown[]) => getAccountHealth(...a),
  checkAccountHealth: (...a: unknown[]) => checkAccountHealth(...a),
  listAccountHealth: (...a: unknown[]) => listAccountHealth(...a),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : String(e)),
    fieldErrors: () => ({}),
  }),
}));

import { useAccountHealth } from "@/features/health/composables/useAccountHealth";
import { useOrgHealth } from "@/features/health/composables/useOrgHealth";

describe("useAccountHealth", () => {
  beforeEach(() => {
    getAccountHealth.mockReset();
    checkAccountHealth.mockReset();
  });

  it("loads and runs check", async () => {
    getAccountHealth.mockResolvedValue({
      accountId: "acc_1",
      severity: "ok",
      summary: "Healthy",
      hints: [],
      facts: {},
      checkedAt: "2026-10-26T00:00:00Z",
      name: "A",
      email: "a@x.com",
      state: "connected",
    });
    checkAccountHealth.mockResolvedValue({
      accountId: "acc_1",
      severity: "ok",
      summary: "Healthy",
      hints: [],
      facts: {},
      checkedAt: "2026-10-26T00:00:00Z",
      name: "A",
      email: "a@x.com",
      state: "connected",
      liveCheck: { ok: true, latencyMs: 12 },
    });
    const id = ref("acc_1");
    const api = useAccountHealth(id);
    await api.load();
    expect(api.health.value?.severity).toBe("ok");
    expect(api.isFirstLoad.value).toBe(false);

    const ok = await api.runCheck();
    expect(ok).toBe(true);
    expect(api.health.value?.liveCheck?.ok).toBe(true);
  });

  it("surfaces load and check errors", async () => {
    getAccountHealth.mockRejectedValue(new Error("boom"));
    const api = useAccountHealth(() => "acc_1");
    await api.load();
    expect(api.loadError.value).toBe("boom");

    checkAccountHealth.mockRejectedValue(new Error("busy"));
    getAccountHealth.mockResolvedValue({
      accountId: "acc_1",
      severity: "ok",
      summary: "Healthy",
      hints: [],
      facts: {},
      checkedAt: "2026-10-26T00:00:00Z",
      name: "A",
      email: "a@x.com",
      state: "connected",
    });
    const ok = await api.runCheck();
    expect(ok).toBe(false);
    expect(api.checkError.value).toBe("busy");
  });
});

describe("useOrgHealth", () => {
  beforeEach(() => {
    listAccountHealth.mockReset();
  });

  it("loads pages and filters", async () => {
    listAccountHealth.mockResolvedValue({
      counts: { ok: 1, info: 0, warning: 1, critical: 0, total: 2 },
      data: [
        {
          accountId: "acc_1",
          name: "A",
          email: "a@x.com",
          state: "connected",
          severity: "ok",
          summary: "Healthy",
          hintCodes: [],
        },
      ],
      total: 1,
      page: 1,
      limit: 25,
    });
    const api = useOrgHealth();
    await api.load();
    expect(api.rows.value).toHaveLength(1);
    expect(api.counts.value.total).toBe(2);

    await api.setSeverity("warning");
    expect(listAccountHealth).toHaveBeenLastCalledWith(
      expect.objectContaining({ severity: "warning", page: 1 }),
    );
    await api.setSearch("ada");
    expect(listAccountHealth).toHaveBeenLastCalledWith(
      expect.objectContaining({ q: "ada" }),
    );
    await api.setPage(2);
    expect(listAccountHealth).toHaveBeenLastCalledWith(
      expect.objectContaining({ page: 2 }),
    );
    await api.setIpp(50);
    expect(listAccountHealth).toHaveBeenLastCalledWith(
      expect.objectContaining({ limit: 50, page: 1 }),
    );
    await api.setSortBy([{ key: "name", order: "asc" }]);
    expect(listAccountHealth).toHaveBeenLastCalledWith(
      expect.objectContaining({ sort: "name" }),
    );
  });

  it("records load errors", async () => {
    listAccountHealth.mockRejectedValue(new Error("down"));
    const api = useOrgHealth();
    await api.load();
    expect(api.loadError.value).toBe("down");
  });
});
