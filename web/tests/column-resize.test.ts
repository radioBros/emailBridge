import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { ref } from "vue";

const patch = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: vi.fn(async () => ({})),
    patch: (...args: unknown[]) => patch(...args),
  },
}));

import { useColumnResize, type ResizeColumn } from "@/composables/useColumnResize";
import { useSettingsStore } from "@/stores/settings";

const sum = (o: Record<string, number>): number => Object.values(o).reduce((s, v) => s + v, 0);

/** A header row whose cells report the given widths; returns the cell of `key`. */
const headerRow = (cols: readonly ResizeColumn[], widths: number[]) => {
  const tr = document.createElement("tr");
  const cells = cols.map((c, i) => {
    const th = document.createElement("th");
    th.dataset.key = c.key;
    th.getBoundingClientRect = () => ({ width: widths[i] }) as DOMRect;
    tr.appendChild(th);
    return th;
  });
  return (key: string): HTMLElement => cells[cols.findIndex((c) => c.key === key)]!;
};

const col = (key: string, extra: Partial<ResizeColumn> = {}): ResizeColumn => ({ key, min: 64, fixed: false, ...extra });

describe("useColumnResize", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    patch.mockReset();
    patch.mockResolvedValue({});
  });

  const setup = (cols: ResizeColumn[], container = 1000) => {
    const columns = ref(cols);
    const r = useColumnResize("t", columns, () => [...columns.value.map((c) => c.key), "hiddenCol"]);
    r.container.value = container;
    return { r, columns, settings: useSettingsStore() };
  };

  it("stays automatic until a resize (nothing saved)", () => {
    const { r } = setup([col("a"), col("b"), col("c")]);
    expect(r.sized.value).toBe(false);
    expect(r.width("a")).toBeUndefined();
    expect(r.totalWidth.value).toBe(0);
  });

  it("handles only where a resizable column follows (last resizable and fixed columns have none)", () => {
    const { r } = setup([col("a"), col("b"), col("c"), col("actions", { fixed: true })]);
    expect(r.canResize("a")).toBe(true);
    expect(r.canResize("b")).toBe(true);
    expect(r.canResize("c")).toBe(false);
    expect(r.canResize("actions")).toBe(false);
  });

  it("first drag freezes the measured layout, fills the box, cascades and saves proportions", () => {
    const cols = [col("a"), col("b"), col("c"), col("actions", { fixed: true })];
    const { r, settings } = setup(cols);
    r.fixedWidths.value = { actions: 100 };
    const th = headerRow(cols, [300, 300, 300, 100]);

    expect(r.begin("a", th("a"))).toBe(true);
    expect(r.sized.value).toBe(true);
    expect(r.widths.value).toEqual({ a: 300, b: 300, c: 300, actions: 100 });

    r.drag("a", 300); // b gives 236, then c gives 64
    expect(r.widths.value).toEqual({ a: 600, b: 64, c: 236, actions: 100 });
    expect(r.totalWidth.value).toBe(1000);

    r.drag("a", 20); // dragging back restores c
    expect(r.widths.value).toEqual({ a: 320, b: 280, c: 300, actions: 100 });

    r.commit();
    const saved = settings.getTable("t")?.widths ?? {};
    expect(Object.keys(saved).sort()).toEqual(["a", "b", "c"]);
    expect(sum(saved)).toBeCloseTo(1, 3);
    expect(patch).toHaveBeenCalledTimes(1);
    // the layout from the saved proportions is the one that was dragged: no jump
    expect(r.widths.value).toEqual({ a: 320, b: 280, c: 300, actions: 100 });
  });

  it("grow clamps when everything to the right is at its minimum", () => {
    const cols = [col("a"), col("b"), col("c")];
    const { r } = setup(cols, 600);
    const th = headerRow(cols, [200, 200, 200]);
    r.begin("a", th("a"));
    r.drag("a", 5000);
    expect(r.widths.value).toEqual({ a: 472, b: 64, c: 64 });
    expect(r.totalWidth.value).toBe(600);
  });

  it("restores saved proportions on any container width and rescales on resize", () => {
    const { r, settings } = setup([col("a"), col("b")], 800);
    r.fixedWidths.value = {};
    settings.tables = { t: { visibility: ["a", "b"], order: ["a", "b"], widths: { a: 0.25, b: 0.75 } } };
    expect(r.sized.value).toBe(true);
    expect(r.widths.value).toEqual({ a: 200, b: 600 });
    r.container.value = 1200;
    expect(r.widths.value).toEqual({ a: 300, b: 900 });
  });

  it("waits for the fixed columns to be measured before switching to px widths", () => {
    const { r, settings } = setup([col("a"), col("b"), col("actions", { fixed: true })], 1000);
    settings.tables = { t: { visibility: [], order: [], widths: { a: 0.5, b: 0.5 } } };
    expect(r.sized.value).toBe(false);
    r.fixedWidths.value = { actions: 120 };
    expect(r.sized.value).toBe(true);
    expect(r.widths.value).toEqual({ a: 440, b: 440, actions: 120 });
  });

  it("a declared fixed width needs no measuring", () => {
    const { r, settings } = setup([col("a"), col("b"), col("x", { fixed: true, width: 50 })], 1050);
    settings.tables = { t: { visibility: [], order: [], widths: { a: 0.5, b: 0.5 } } };
    expect(r.widths.value).toEqual({ a: 500, b: 500, x: 50 });
  });

  it("hiding / showing a column rescales; a new column gets its default share", () => {
    const { r, columns, settings } = setup([col("a"), col("b")], 900);
    settings.tables = { t: { visibility: [], order: [], widths: { a: 0.5, b: 0.5 } } };
    columns.value = [col("a"), col("new"), col("b")];
    expect(r.widths.value).toEqual({ a: 300, new: 300, b: 300 });
    columns.value = [col("b")];
    expect(r.widths.value).toEqual({ b: 900 });
  });

  it("hidden columns keep their saved share across a resize", () => {
    const { r, settings } = setup([col("a"), col("b")], 1000);
    settings.tables = { t: { visibility: [], order: [], widths: { a: 0.25, b: 0.25, hiddenCol: 0.5 } } };
    expect(r.widths.value).toEqual({ a: 500, b: 500 });
    r.begin("a", document.createElement("th"));
    r.drag("a", 100);
    r.commit();
    const saved = settings.getTable("t")?.widths ?? {};
    expect(saved.hiddenCol).toBeCloseTo(0.5, 4);
    expect(saved.a).toBeCloseTo(0.3, 4);
    expect(saved.b).toBeCloseTo(0.2, 4);
  });

  it("overflow only when the minimums do not fit", () => {
    const { r, settings } = setup([col("a", { min: 200 }), col("b", { min: 200 })], 300);
    settings.tables = { t: { visibility: [], order: [], widths: { a: 0.5, b: 0.5 } } };
    expect(r.totalWidth.value).toBe(400);
  });

  it("keyboard steps resize with the same neighbour rules", () => {
    const cols = [col("a"), col("b"), col("c")];
    const { r, settings } = setup(cols, 600);
    const th = headerRow(cols, [200, 200, 200]);
    r.step("b", 16, th("b"));
    expect(r.widths.value).toEqual({ a: 200, b: 216, c: 184 });
    r.step("b", -64, th("b"));
    expect(r.widths.value).toEqual({ a: 200, b: 152, c: 248 });
    expect(patch).not.toHaveBeenCalled();
    r.commit();
    expect(patch).toHaveBeenCalledTimes(1);
    expect(settings.getTable("t")?.widths?.b).toBeCloseTo(152 / 600, 3);
  });

  it("cancel drops an unsaved drag", () => {
    const cols = [col("a"), col("b")];
    const { r } = setup(cols, 400);
    r.begin("a", headerRow(cols, [200, 200])("a"));
    r.drag("a", 50);
    r.cancel();
    expect(r.sized.value).toBe(false);
    expect(patch).not.toHaveBeenCalled();
  });

  it("reset gives a column its default share and saves", () => {
    const { r, settings } = setup([col("a"), col("b"), col("c")], 900);
    settings.tables = { t: { visibility: [], order: [], widths: { a: 0.6, b: 0.2, c: 0.2 } } };
    expect(r.widths.value).toEqual({ a: 540, b: 180, c: 180 });
    r.reset("a", document.createElement("th"));
    expect(r.widths.value).toEqual({ a: 300, b: 420, c: 180 });
    expect(patch).toHaveBeenCalledTimes(1);
  });

  it("ARIA range: current width, minimum and the reachable maximum", () => {
    const { r, settings } = setup([col("a"), col("b"), col("c")], 600);
    expect(r.range("a")).toEqual({ min: 64 });
    settings.tables = { t: { visibility: [], order: [], widths: { a: 1, b: 1, c: 1 } } };
    // equal weights → thirds
    expect(r.range("a")).toEqual({ now: 200, min: 64, max: 472 });
  });

  it("the store PATCH deletes saved keys that are no longer in the map", () => {
    const settings = useSettingsStore();
    settings.tables = { t: { visibility: ["a"], order: ["a"], widths: { a: 280, gone: 100 } } };
    settings.setWidths("t", { a: 1 });
    expect(settings.getTable("t")?.widths).toEqual({ a: 1 });
    expect(patch).toHaveBeenCalledWith("/me/settings", {
      tables: { t: { visibility: ["a"], order: ["a"], widths: { a: 1, gone: null } } },
    });
  });
});
