import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

const listExports = vi.fn();
const createExport = vi.fn();
const cancelExport = vi.fn();
const listFeatureSwitches = vi.fn();
const listMailboxes = vi.fn();

vi.mock("@/api/exports", () => ({
  listExports: (...a: unknown[]) => listExports(...a),
  createExport: (...a: unknown[]) => createExport(...a),
  cancelExport: (...a: unknown[]) => cancelExport(...a),
}));

vi.mock("@/api/settings", () => ({
  listFeatureSwitches: (...a: unknown[]) => listFeatureSwitches(...a),
}));

vi.mock("@/api/messages", () => ({
  listMailboxes: (...a: unknown[]) => listMailboxes(...a),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : String(e)),
    fieldErrors: () => ({}),
  }),
}));

import {
  sortParam,
  useAccountExports,
} from "@/features/exports/composables/useAccountExports";

describe("useAccountExports", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    listExports.mockReset();
    createExport.mockReset();
    cancelExport.mockReset();
    listFeatureSwitches.mockReset();
    listMailboxes.mockReset();
  });

  it("sortParam defaults and flips", () => {
    expect(sortParam([])).toBe("-createdAt");
    expect(sortParam([{ key: "id", order: "asc" }])).toBe("id");
    expect(sortParam([{ key: "createdAt", order: "desc" }])).toBe("-createdAt");
    expect(sortParam([{ key: "nope", order: "asc" }])).toBe("-createdAt");
  });

  it("skips list when feature disabled", async () => {
    listFeatureSwitches.mockResolvedValue({
      data: [{ name: "export.enabled", effective: false }],
    });
    const api = useAccountExports(() => "acc_1");
    await api.load();
    expect(api.enabled.value).toBe(false);
    expect(listExports).not.toHaveBeenCalled();
    expect(api.rows.value).toEqual([]);
  });

  it("loads, creates and cancels when enabled", async () => {
    listFeatureSwitches.mockResolvedValue({
      data: [{ name: "export.enabled", effective: true }],
    });
    listExports.mockResolvedValue({
      items: [
        {
          id: "exp_1",
          organizationId: "org",
          accountId: "acc_1",
          state: "queued",
          filters: { include: { metadataOnly: true } },
          createdAt: "2026-10-26T00:00:00Z",
        },
      ],
      total: 1,
    });
    createExport.mockResolvedValue({
      export: {
        id: "exp_2",
        organizationId: "org",
        accountId: "acc_1",
        state: "queued",
        filters: { include: { metadataOnly: true } },
        createdAt: "2026-10-26T00:00:00Z",
      },
    });
    cancelExport.mockResolvedValue({ id: "exp_1", state: "cancelled" });
    listMailboxes.mockResolvedValue({ data: [{ id: "mbx_1", path: "INBOX" }] });

    const api = useAccountExports(() => "acc_1");
    await api.load();
    expect(api.enabled.value).toBe(true);
    expect(api.rows.value).toHaveLength(1);
    expect(listExports).toHaveBeenCalled();

    await api.loadMailboxes();
    expect(api.mailboxes.value[0]?.path).toBe("INBOX");

    const created = await api.create({ include: { metadataOnly: true } });
    expect(created?.id).toBe("exp_2");
    expect(createExport).toHaveBeenCalled();

    expect(await api.cancel("exp_1")).toBe(true);
    expect(cancelExport).toHaveBeenCalledWith("acc_1", "exp_1");

    await api.setPage(2);
    await api.setIpp(10);
    await api.setSortBy([{ key: "id", order: "asc" }]);
    expect(listExports.mock.calls.length).toBeGreaterThan(3);
  });

  it("surfaces create errors", async () => {
    listFeatureSwitches.mockResolvedValue({
      data: [{ name: "export.enabled", effective: true }],
    });
    listExports.mockResolvedValue({ items: [], total: 0 });
    createExport.mockRejectedValue(new Error("boom"));
    const api = useAccountExports(() => "acc_1");
    await api.load();
    const created = await api.create({ include: { raw: true } });
    expect(created).toBeNull();
    expect(api.formError.value).toBe("boom");
  });
});
