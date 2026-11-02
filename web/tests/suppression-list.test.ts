import { beforeEach, describe, expect, it, vi } from "vitest";
import {
  sortParam,
  useSuppressionLists,
} from "@/features/suppression/composables/useSuppressionLists";
import { useSuppressionEntries } from "@/features/suppression/composables/useSuppressionEntries";
import { kindColor, kindLabelKey, sourceLabelKey } from "@/features/suppression/helpers/kinds";

vi.mock("@/api/suppression", () => ({
  listSuppressionLists: vi.fn(),
  createSuppressionList: vi.fn(),
  patchSuppressionList: vi.fn(),
  deleteSuppressionList: vi.fn(),
  getSuppressionList: vi.fn(),
  listSuppressionEntries: vi.fn(),
  createSuppressionEntry: vi.fn(),
  deleteSuppressionEntry: vi.fn(),
  importSuppressionEntries: vi.fn(),
  exportSuppressionEntries: vi.fn(),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : "err"),
    fieldErrors: () => ({}),
  }),
}));

import {
  createSuppressionEntry,
  createSuppressionList,
  deleteSuppressionList,
  exportSuppressionEntries,
  getSuppressionList,
  importSuppressionEntries,
  listSuppressionEntries,
  listSuppressionLists,
  patchSuppressionList,
} from "@/api/suppression";

const list = {
  id: "sup_1",
  organizationId: "org_1",
  name: "Block",
  kind: "manual",
  createdAt: "2026-10-01T00:00:00Z",
  updatedAt: "2026-10-25T00:00:00Z",
  version: 1,
};

const entry = {
  id: "sup_1:a@b.test",
  listId: "sup_1",
  organizationId: "org_1",
  address: "a@b.test",
  source: "manual",
  at: "2026-10-26T00:00:00Z",
};

describe("useSuppressionLists", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("sortParam and CRUD", async () => {
    expect(sortParam([{ key: "name", order: "desc" }])).toBe("-name");
    vi.mocked(listSuppressionLists).mockResolvedValue({ data: [list], nextCursor: null, total: 1 });
    vi.mocked(createSuppressionList).mockResolvedValue(list);
    vi.mocked(patchSuppressionList).mockResolvedValue({ ...list, name: "Blocked" });
    vi.mocked(deleteSuppressionList).mockResolvedValue(undefined);

    const w = useSuppressionLists();
    await w.load();
    await w.setKind("manual");
    expect(listSuppressionLists).toHaveBeenCalledWith(expect.objectContaining({ kind: "manual" }));
    await w.create({ name: "Block" });
    await w.update("sup_1", { name: "Blocked" });
    expect(await w.remove("sup_1")).toBe(true);
  });
});

describe("useSuppressionEntries", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads list + entries + csv", async () => {
    vi.mocked(getSuppressionList).mockResolvedValue(list);
    vi.mocked(listSuppressionEntries).mockResolvedValue({
      data: [entry],
      nextCursor: null,
      total: 1,
    });
    vi.mocked(createSuppressionEntry).mockResolvedValue(entry);
    vi.mocked(importSuppressionEntries).mockResolvedValue({
      imported: 1,
      skipped: 0,
      invalid: 0,
    });
    vi.mocked(exportSuppressionEntries).mockResolvedValue("a@b.test,\n");

    const w = useSuppressionEntries();
    expect(await w.loadList("sup_1")).toBe(true);
    await w.load("sup_1");
    expect(w.rows.value).toHaveLength(1);
    await w.add("sup_1", { address: "a@b.test" });
    const imp = await w.importCsv("sup_1", "a@b.test\n");
    expect(imp?.imported).toBe(1);
    expect(await w.exportCsv("sup_1")).toContain("a@b.test");
  });
});

describe("suppression kinds", () => {
  it("labels and colours", () => {
    expect(kindLabelKey("bounce")).toContain("bounce");
    expect(sourceLabelKey("import")).toContain("import");
    expect(kindColor("complaint")).toBe("error");
    expect(kindColor("manual")).toBe("secondary");
  });
});
