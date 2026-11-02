import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

const get = vi.fn();
const patch = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    patch: (...args: unknown[]) => patch(...args),
  },
}));

describe("settings store", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    get.mockReset();
    patch.mockReset();
    get.mockResolvedValue({ tables: { t1: { visibility: ["a"], order: ["a", "b"] } } });
    patch.mockResolvedValue({});
  });

  it("loads and saves table slices via /me/settings", async () => {
    const { useSettingsStore } = await import("@/stores/settings");
    const store = useSettingsStore();
    await store.load();
    expect(get).toHaveBeenCalledWith("/me/settings");
    expect(store.getTable("t1")?.visibility).toEqual(["a"]);

    store.setVisibility("t1", ["b"], ["a", "b"]);
    store.setOrder("t1", ["b", "a"]);
    store.setWidths("t1", { a: 120 });
    store.resetWidths("t1");
    expect(patch).toHaveBeenCalled();
    store.reset();
    expect(store.tables).toEqual({});
  });

  it("reads the tables from the API's { settings, version } envelope", async () => {
    get.mockResolvedValue({
      settings: { tables: { t1: { visibility: ["a"], order: ["a"], widths: { a: 1 } } } },
      updatedAt: "2026-10-30T00:00:00Z",
      version: 3,
    });
    const { useSettingsStore } = await import("@/stores/settings");
    const store = useSettingsStore();
    await store.load();
    expect(store.getTable("t1")?.widths).toEqual({ a: 1 });
  });
});
