import { describe, expect, it } from "vitest";
import {
  MIN_COLUMN_WIDTH,
  cleanProportions,
  columnMin,
  fit,
  fromProportions,
  isFixedColumn,
  mergeProportions,
  parsePx,
  resize,
  resizeRange,
  roundToTotal,
  toProportions,
} from "@/helpers/ColumnWidths";

const total = (a: readonly number[]): number => a.reduce((s, v) => s + v, 0);
const mins = (n: number, m = 64): number[] => new Array<number>(n).fill(m);

describe("ColumnWidths — headers", () => {
  it("fixed columns: actions, data-table-*, resizable:false", () => {
    expect(isFixedColumn({ key: "actions" })).toBe(true);
    expect(isFixedColumn({ key: "_actions" })).toBe(true);
    expect(isFixedColumn({ key: "data-table-select" })).toBe(true);
    expect(isFixedColumn({ key: "flag", resizable: false })).toBe(true);
    expect(isFixedColumn({ key: "email" })).toBe(false);
    expect(isFixedColumn({ key: "email", resizable: true })).toBe(false);
  });

  it("minimum: header minWidth (number or px string), else the default", () => {
    expect(MIN_COLUMN_WIDTH).toBe(64);
    expect(columnMin({ key: "a" })).toBe(64);
    expect(columnMin({ key: "a", minWidth: 120 })).toBe(120);
    expect(columnMin({ key: "a", minWidth: "90px" })).toBe(90);
    expect(columnMin({ key: "a", minWidth: 40.2 })).toBe(41);
    expect(columnMin({ key: "a", minWidth: "10rem" })).toBe(64);
    expect(parsePx("12")).toBe(12);
    expect(parsePx(-1)).toBeNull();
    expect(parsePx(Number.NaN)).toBeNull();
    expect(parsePx(undefined)).toBeNull();
  });
});

describe("ColumnWidths — roundToTotal", () => {
  it("sums exactly to the total by largest remainder", () => {
    expect(roundToTotal([100.4, 100.4, 100.2], 301)).toEqual([101, 100, 100]);
    expect(roundToTotal([33.333, 33.333, 33.334], 100)).toEqual([33, 33, 34]);
    const r = roundToTotal([10.9, 20.9, 30.2], 62);
    expect(total(r)).toBe(62);
    expect(r).toEqual([11, 21, 30]);
  });
});

describe("ColumnWidths — fit", () => {
  it("scales proportionally to fill the container exactly", () => {
    expect(fit([100, 200, 100], mins(3), 800)).toEqual([200, 400, 200]);
    expect(fit([200, 400, 200], mins(3), 400)).toEqual([100, 200, 100]);
  });

  it("container grows and shrinks: always integers summing to the container", () => {
    for (const container of [333, 500, 777, 1001, 1919, 2560]) {
      const w = fit([123, 456, 78.5, 300], mins(4), container);
      expect(total(w)).toBe(container);
      w.forEach((x) => expect(Number.isInteger(x)).toBe(true));
    }
  });

  it("never goes below a minimum; the rest is shared by the others", () => {
    const w = fit([10, 500, 500], [64, 64, 64], 1000);
    expect(w[0]).toBe(64);
    expect(total(w)).toBe(1000);
    expect(w[1]).toBe(468);
    expect(w[2]).toBe(468);
  });

  it("cascading minimums (water-filling) still sum exactly", () => {
    const w = fit([1, 2, 1000, 3], [80, 100, 64, 90], 600);
    expect(w).toEqual([80, 100, 330, 90]);
    expect(total(w)).toBe(600);
  });

  it("respects per-column minimums", () => {
    const w = fit([100, 100, 100], [200, 64, 64], 450);
    expect(w[0]).toBe(200);
    expect(total(w)).toBe(450);
  });

  it("overflow: when the minimums do not fit, each gets its minimum or its share, whichever is larger", () => {
    const w = fit([100, 100, 400], mins(3, 100), 250);
    // shares of 250: 41.7, 41.7, 166.7 → mins 100, 100, then 167
    expect(w).toEqual([100, 100, 167]);
    expect(total(w)).toBeGreaterThan(250);
  });

  it("invalid weights count as zero; all-invalid means equal shares", () => {
    expect(fit([0, Number.NaN, -5], mins(3), 300)).toEqual([100, 100, 100]);
    expect(fit([0, 300], mins(2), 300)).toEqual([64, 236]);
  });

  it("empty", () => {
    expect(fit([], [], 500)).toEqual([]);
  });
});

describe("ColumnWidths — resize (drag the right border of column N)", () => {
  const m = mins(4);

  it("grow: the immediate right neighbour absorbs first", () => {
    expect(resize([200, 200, 200, 200], m, 0, 50)).toEqual([250, 150, 200, 200]);
  });

  it("grow: cascades to the next column once the neighbour hits its minimum", () => {
    expect(resize([200, 100, 200, 200], m, 0, 100)).toEqual([300, 64, 136, 200]);
  });

  it("grow: cascades over several neighbours", () => {
    expect(resize([100, 80, 70, 300], m, 0, 100)).toEqual([200, 64, 64, 222]);
  });

  it("grow: clamps when every column to the right is at its minimum (no overflow)", () => {
    const out = resize([200, 100, 100, 100], m, 0, 1000);
    expect(out).toEqual([308, 64, 64, 64]);
    expect(total(out)).toBe(500);
  });

  it("shrink: all the freed space goes to the immediate right neighbour", () => {
    expect(resize([300, 100, 100, 100], m, 0, -100)).toEqual([200, 200, 100, 100]);
    expect(resize([300, 100, 100, 100], m, 1, -30)).toEqual([300, 70, 130, 100]);
  });

  it("shrink: clamps at the column's own minimum", () => {
    expect(resize([100, 100, 100, 100], m, 0, -500)).toEqual([64, 136, 100, 100]);
  });

  it("the last column has nothing to its right: unchanged", () => {
    expect(resize([100, 100, 100], mins(3), 2, 50)).toEqual([100, 100, 100]);
    expect(resize([100, 100, 100], mins(3), 2, -50)).toEqual([100, 100, 100]);
  });

  it("only columns to the RIGHT absorb, never those to the left", () => {
    expect(resize([200, 200, 200, 200], m, 2, 100)).toEqual([200, 200, 300, 100]);
    // the neighbour bottoms out and there is nothing further right: clamped, the left columns untouched
    expect(resize([200, 200, 200, 200], m, 2, 400)).toEqual([200, 200, 336, 64]);
  });

  it("the total never changes", () => {
    const start = [180, 95, 240, 110];
    for (const d of [-400, -50, -1, 1, 37, 500]) {
      for (let i = 0; i < 4; i++) expect(total(resize(start, m, i, d))).toBe(total(start));
    }
  });

  it("drag back from the start restores the cascaded neighbours", () => {
    const start = [200, 100, 200, 200];
    resize(start, m, 0, 150); // a frame mid-drag
    expect(resize(start, m, 0, 0)).toEqual(start);
    expect(resize(start, m, 0, 20)).toEqual([220, 80, 200, 200]);
  });

  it("respects per-column minimums while cascading", () => {
    expect(resize([200, 150, 200], [64, 120, 150], 0, 200)).toEqual([280, 120, 150]);
  });

  it("rounds a fractional delta and ignores out-of-range indexes", () => {
    expect(resize([200, 200], mins(2), 0, 10.6)).toEqual([211, 189]);
    expect(resize([200, 200], mins(2), -1, 10)).toEqual([200, 200]);
    expect(resize([200, 200], mins(2), 5, 10)).toEqual([200, 200]);
  });

  it("resizeRange: min and max the handle can reach", () => {
    expect(resizeRange([200, 100, 100], mins(3), 0)).toEqual({ min: 64, max: 272 });
    expect(resizeRange([200, 100, 100], mins(3), 2)).toEqual({ min: 100, max: 100 });
  });
});

describe("ColumnWidths — proportions", () => {
  it("toProportions: fractions of the sum, 4 decimals", () => {
    expect(toProportions(["a", "b", "c"], [250, 500, 250])).toEqual({ a: 0.25, b: 0.5, c: 0.25 });
    expect(toProportions(["a", "b", "c"], [1, 1, 1])).toEqual({ a: 0.3333, b: 0.3333, c: 0.3333 });
  });

  it("round trip px → proportions → px reproduces the widths exactly (no jump after a commit)", () => {
    for (const container of [640, 1000, 1366, 1920, 2557]) {
      const keys = ["a", "b", "c", "d", "e"];
      const px = fit([137, 64, 402, 99, 300], mins(5), container);
      const back = fromProportions(keys, toProportions(keys, px), mins(5), container);
      expect(back).toEqual(px);
    }
  });

  it("fromProportions fills a different screen size", () => {
    const stored = { a: 0.25, b: 0.5, c: 0.25 };
    expect(fromProportions(["a", "b", "c"], stored, mins(3), 800)).toEqual([200, 400, 200]);
    expect(fromProportions(["a", "b", "c"], stored, mins(3), 1600)).toEqual([400, 800, 400]);
  });

  it("hidden column: the visible ones are renormalised to fill the width", () => {
    const stored = { a: 0.25, b: 0.5, c: 0.25 };
    expect(fromProportions(["a", "b"], stored, mins(2), 900)).toEqual([300, 600]);
  });

  it("a newly shown column gets the default share (1/n); the others scale into the rest", () => {
    const stored = { a: 0.5, b: 0.5 };
    expect(fromProportions(["a", "b", "new"], stored, mins(3), 900)).toEqual([300, 300, 300]);
    const s2 = { a: 0.75, b: 0.25 };
    expect(fromProportions(["a", "new", "b"], s2, mins(3), 1200)).toEqual([600, 400, 200]);
  });

  it("nothing saved: equal shares", () => {
    expect(fromProportions(["a", "b", "c"], undefined, mins(3), 300)).toEqual([100, 100, 100]);
  });

  it("order does not matter: proportions are keyed", () => {
    const stored = { a: 0.2, b: 0.8 };
    expect(fromProportions(["b", "a"], stored, mins(2), 1000)).toEqual([800, 200]);
  });

  it("minimums still win over proportions", () => {
    expect(fromProportions(["a", "b"], { a: 0.01, b: 0.99 }, [100, 64], 1000)).toEqual([100, 900]);
  });

  it("cleanProportions: legacy px become fractions; stray px next to fractions are dropped; junk ignored", () => {
    expect(cleanProportions({ a: 280, b: 120 })).toEqual({ a: 0.7, b: 0.3 });
    expect(cleanProportions({ a: 0.6, b: 0.4, old: 280 })).toEqual({ a: 0.6, b: 0.4 });
    expect(cleanProportions({ a: 0.5, b: "x", c: -1, d: Number.NaN, e: null })).toEqual({ a: 0.5 });
    expect(cleanProportions(undefined)).toEqual({});
  });

  it("legacy px layouts still restore and fill", () => {
    expect(fromProportions(["a", "b"], { a: 280, b: 120 }, mins(2), 1000)).toEqual([700, 300]);
  });

  it("mergeProportions: hidden columns keep their share, visible ones take the rest", () => {
    const stored = { a: 0.25, b: 0.25, c: 0.25, d: 0.25 };
    const merged = mergeProportions(stored, { a: 0.5, b: 0.25, c: 0.25 }, ["a", "b", "c", "d"]);
    expect(merged).toEqual({ a: 0.375, b: 0.1875, c: 0.1875, d: 0.25 });
    // showing d again gives it its old quarter
    expect(fromProportions(["a", "b", "c", "d"], merged, mins(4), 1000)).toEqual([375, 188, 187, 250]);
  });

  it("mergeProportions: keys the table no longer has are dropped", () => {
    const merged = mergeProportions({ a: 0.5, gone: 0.5 }, { a: 1 }, ["a"]);
    expect(merged).toEqual({ a: 1 });
  });

  it("mergeProportions: a legacy px layout is converted on the first save", () => {
    const merged = mergeProportions({ a: 200, b: 200, h: 400 }, { a: 0.5, b: 0.5 }, ["a", "b", "h"]);
    expect(merged).toEqual({ a: 0.25, b: 0.25, h: 0.5 });
  });

  it("mergeProportions: hidden columns never take (almost) all the width", () => {
    const merged = mergeProportions({ h: 1 }, { a: 1 }, ["a", "h"]);
    expect(merged.a).toBeCloseTo(0.1, 4);
    expect(merged.h).toBeCloseTo(0.9, 4);
  });
});
