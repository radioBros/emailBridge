import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => (err instanceof Error ? err.message : String(err)),
    fieldErrors: () => ({}),
  }),
}));

const listDrafts = vi.fn();
const getDraft = vi.fn();

vi.mock("@/api/messages", () => ({
  listDrafts: (...a: unknown[]) => listDrafts(...a),
  getDraft: (...a: unknown[]) => getDraft(...a),
}));

import { useDrafts } from "@/features/messages/composables/useDrafts";

describe("useDrafts", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    listDrafts.mockResolvedValue({
      data: [
        {
          id: "drf_1",
          accountId: "acc_1",
          path: "Drafts",
          subject: "WIP",
          size: 10,
          createdAt: "2026-10-26T09:00:00Z",
          updatedAt: "2026-10-26T09:00:00Z",
        },
      ],
    });
    getDraft.mockResolvedValue({
      id: "drf_1",
      accountId: "acc_1",
      path: "Drafts",
      subject: "WIP",
      size: 10,
      createdAt: "2026-10-26T09:00:00Z",
      updatedAt: "2026-10-26T09:00:00Z",
      text: "draft body",
    });
  });

  it("lists and opens a draft read-only", async () => {
    const d = useDrafts();
    await d.load("acc_1");
    expect(d.items.value).toHaveLength(1);
    await d.show("acc_1", "drf_1");
    expect(getDraft).toHaveBeenCalledWith("acc_1", "drf_1", { text: true });
    expect(d.detail.value?.text).toBe("draft body");
    expect(d.open.value).toBe(true);
    d.close();
    expect(d.open.value).toBe(false);
  });
});
