import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
    raw: {
      post: vi.fn(),
      get: vi.fn(),
    },
  },
}));

import { api } from "@/api/client";
import {
  createSuppressionEntry,
  createSuppressionList,
  deleteSuppressionEntry,
  deleteSuppressionList,
  exportSuppressionEntries,
  importSuppressionEntries,
  listSuppressionEntries,
  listSuppressionLists,
  patchSuppressionList,
} from "@/api/suppression";

describe("suppression api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists and entries", async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [], total: 0 });
    await listSuppressionLists({ page: 1, kind: "manual", q: "block" });
    await listSuppressionEntries("sup_1", { page: 1, q: "a@" });
    expect(api.get).toHaveBeenCalledWith(expect.stringContaining("/suppression-lists"));
    expect(api.get).toHaveBeenCalledWith(
      expect.stringContaining("/suppression-lists/sup_1/entries"),
    );
  });

  it("CRUD + csv", async () => {
    vi.mocked(api.post).mockResolvedValue({ id: "sup_1" });
    vi.mocked(api.patch).mockResolvedValue({ id: "sup_1" });
    vi.mocked(api.delete).mockResolvedValue(undefined);
    vi.mocked(api.raw.post).mockResolvedValue({ data: { imported: 1, skipped: 0, invalid: 0 } });
    vi.mocked(api.raw.get).mockResolvedValue({ data: "a@b.test,reason\n" });

    await createSuppressionList({ name: "Block" });
    await patchSuppressionList("sup_1", { name: "Blocked" });
    await createSuppressionEntry("sup_1", { address: "a@b.test" });
    await deleteSuppressionEntry("sup_1", "sup_1:a@b.test");
    await deleteSuppressionList("sup_1");
    const imp = await importSuppressionEntries("sup_1", "a@b.test\n");
    expect(imp.imported).toBe(1);
    const csv = await exportSuppressionEntries("sup_1");
    expect(csv).toContain("a@b.test");
  });
});
