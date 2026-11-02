// =========================================================================
// ColumnWidths — the pure width maths behind resizable table columns.
//
// Framework-free so every rule can be unit-tested without a DOM:
//
//   • fit()             widths scaled to fill a container, never below a
//                       column's minimum; integers that sum to the container
//                       exactly (only when the minimums do not fit does the
//                       sum exceed it — the one case with horizontal scroll)
//   • resize()          a drag of column N's right border: N changes by Δ and
//                       the columns to its RIGHT absorb the opposite change —
//                       growing takes from the immediate neighbour first, then
//                       the next once it reaches its minimum (cascade);
//                       shrinking gives everything to the immediate neighbour;
//                       clamped, so the total never changes
//   • toProportions() / fromProportions() / mergeProportions()
//                       what is persisted: fractions of the resizable width,
//                       so a layout saved on one screen fills another
//
// Every array is aligned to the RESIZABLE columns of the table, in display
// order; fixed columns (actions, checkboxes) are left out by the caller.
// =========================================================================

import { isActionKey } from './TableColumns';

/** Narrowest a column can be resized to, in px, unless its header sets `minWidth`. */
export const MIN_COLUMN_WIDTH = 64;

/** Decimal places a stored proportion keeps (0.0001 of a 2000 px table is 0.2 px). */
const PROPORTION_DECIMALS = 4;

const sum = (values: readonly number[]): number => values.reduce((s, v) => s + v, 0);

const isPositive = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v) && v > 0;

export interface WidthHeader {
  key: string;
  minWidth?: unknown;
  resizable?: unknown;
  [key: string]: unknown;
}

/**
 * Whether a header is a fixed column — sized to its content, never resized and
 * never used to absorb a resize: the actions column, Vuetify's own
 * `data-table-*` columns (select, expand) and any header with `resizable: false`.
 */
export const isFixedColumn = (h: WidthHeader): boolean =>
  isActionKey(h.key) || h.key.startsWith('data-table-') || h.resizable === false;

/** A px length from a header value (`120` or `'120px'`), else null. */
export const parsePx = (v: unknown): number | null => {
  if (typeof v === 'number') return Number.isFinite(v) && v >= 0 ? v : null;
  if (typeof v === 'string' && /^\s*\d+(\.\d+)?\s*(px)?\s*$/.test(v)) return Number.parseFloat(v);
  return null;
};

/** A header's minimum width: its own `minWidth` when it sets one, else MIN_COLUMN_WIDTH. */
export const columnMin = (h: WidthHeader): number => Math.ceil(parsePx(h.minWidth) ?? MIN_COLUMN_WIDTH);

/**
 * Rounds to integers that sum to `total` exactly (largest remainder). Every
 * value is floored first, so an integer lower bound a value already met is
 * never broken.
 */
export const roundToTotal = (values: readonly number[], total: number): number[] => {
  const floors = values.map((v) => Math.floor(v));
  let missing = Math.round(total - sum(floors));
  const order = values
    .map((v, i) => ({ i, rest: v - Math.floor(v) }))
    .sort((a, b) => b.rest - a.rest || a.i - b.i);
  for (let k = 0; missing > 0 && order.length > 0; k = (k + 1) % order.length, missing--) {
    floors[order[k]!.i]! += 1;
  }
  return floors;
};

/** Weights that can be scaled: invalid or non-positive ones count as 0; all-zero becomes equal. */
const weightsOf = (widths: readonly number[]): number[] => {
  const w = widths.map((v) => (isPositive(v) ? v : 0));
  return sum(w) > 0 ? w : w.map(() => 1);
};

/**
 * Scales `widths` to fill `container`, keeping their proportions, never below
 * `mins`. The result is integers summing to the container exactly. When the
 * minimums alone exceed the container, each column gets its minimum or its
 * proportional share, whichever is larger (the sum then exceeds the container:
 * the table scrolls sideways).
 */
export const fit = (widths: readonly number[], mins: readonly number[], container: number): number[] => {
  const n = widths.length;
  if (n === 0) return [];
  const lo = widths.map((_, i) => Math.max(0, Math.ceil(mins[i] ?? MIN_COLUMN_WIDTH)));
  const target = Math.max(0, Math.floor(container));
  const weights = weightsOf(widths);
  const total = sum(weights);

  if (sum(lo) >= target) {
    return weights.map((w, i) => Math.max(lo[i]!, Math.round((w / total) * target)));
  }

  // Water-filling: a column whose share would fall below its minimum is pinned
  // there and the rest is shared again among the others.
  const pinned = new Array<boolean>(n).fill(false);
  let scale = 0;
  for (;;) {
    const pinnedSum = sum(lo.filter((_, i) => pinned[i]));
    const freeWeight = sum(weights.filter((_, i) => !pinned[i]));
    if (freeWeight <= 0) break;
    scale = (target - pinnedSum) / freeWeight;
    let changed = false;
    weights.forEach((w, i) => {
      if (!pinned[i] && w * scale < lo[i]!) {
        pinned[i] = true;
        changed = true;
      }
    });
    if (!changed) break;
  }
  return roundToTotal(
    weights.map((w, i) => (pinned[i] ? lo[i]! : w * scale)),
    target,
  );
};

/**
 * Column `index`'s right border dragged by `delta` px. The columns to its right
 * absorb the opposite change, so the total stays the same:
 *
 *   • growing takes from the immediate right neighbour first, then from the
 *     next once it reaches its minimum, and so on; when every column to the
 *     right is at its minimum the column cannot grow further;
 *   • shrinking (down to the column's minimum) gives all the freed space to the
 *     immediate right neighbour.
 *
 * The last column has nothing to its right: it never changes. Apply a drag as
 * resize(widthsAtDragStart, mins, index, totalDelta) — never step by step — so
 * dragging back restores the neighbours the cascade shrank.
 */
export const resize = (
  widths: readonly number[],
  mins: readonly number[],
  index: number,
  delta: number,
): number[] => {
  const out = [...widths];
  const d = Math.round(delta);
  if (index < 0 || index >= out.length - 1 || d === 0) return out;
  const min = (i: number): number => Math.ceil(mins[i] ?? MIN_COLUMN_WIDTH);

  if (d > 0) {
    let need = d;
    for (let j = index + 1; j < out.length && need > 0; j++) {
      const take = Math.min(need, Math.max(0, out[j]! - min(j)));
      out[j]! -= take;
      need -= take;
    }
    out[index]! += d - need;
  } else {
    const give = Math.min(-d, Math.max(0, out[index]! - min(index)));
    out[index]! -= give;
    out[index + 1]! += give;
  }
  return out;
};

/** The widths column `index` can reach with resize(): its minimum, and its width plus all the slack to its right. */
export const resizeRange = (
  widths: readonly number[],
  mins: readonly number[],
  index: number,
): { min: number; max: number } => {
  const now = widths[index] ?? 0;
  const min = Math.min(now, Math.ceil(mins[index] ?? MIN_COLUMN_WIDTH));
  if (index < 0 || index >= widths.length - 1) return { min: now, max: now };
  let slack = 0;
  for (let j = index + 1; j < widths.length; j++) {
    slack += Math.max(0, widths[j]! - Math.ceil(mins[j] ?? MIN_COLUMN_WIDTH));
  }
  return { min, max: now + slack };
};

const roundProportion = (v: number): number => {
  const f = 10 ** PROPORTION_DECIMALS;
  return Math.round(v * f) / f;
};

/** Each column's fraction of the sum of `widths`, keyed by column. */
export const toProportions = (keys: readonly string[], widths: readonly number[]): Record<string, number> => {
  const weights = weightsOf(keys.map((_, i) => widths[i] ?? 0));
  const total = sum(weights);
  return Object.fromEntries(keys.map((k, i) => [k, roundProportion(weights[i]! / total)]));
};

/**
 * A saved widths map made safe to use: only positive finite numbers survive.
 * Fractions are ≤ 1 and px widths are not, so an older px layout (every value
 * > 1) is turned into fractions of its own sum; px values left behind next to
 * fractions are dropped.
 */
export const cleanProportions = (stored: Record<string, unknown> | null | undefined): Record<string, number> => {
  const entries = Object.entries(stored ?? {}).filter((e): e is [string, number] => isPositive(e[1]));
  if (entries.length === 0) return {};
  const fractions = entries.filter(([, v]) => v <= 1);
  if (fractions.length > 0) return Object.fromEntries(fractions);
  const total = sum(entries.map(([, v]) => v));
  return Object.fromEntries(entries.map(([k, v]) => [k, v / total]));
};

/**
 * Px widths for the resizable `keys` from saved proportions, filling
 * `container`. Only the given (visible) keys count, renormalised among
 * themselves, so hiding a column scales the others up. A key with nothing
 * saved (a newly shown column) gets the default share, 1/n of the width.
 */
export const fromProportions = (
  keys: readonly string[],
  stored: Record<string, unknown> | null | undefined,
  mins: readonly number[],
  container: number,
): number[] => {
  const n = keys.length;
  if (n === 0) return [];
  const saved = cleanProportions(stored);
  const known = keys.filter((k) => saved[k] !== undefined);
  const knownSum = sum(known.map((k) => saved[k]!));
  const knownShare = known.length / n;
  const shares = keys.map((k) =>
    saved[k] !== undefined && knownSum > 0 ? (saved[k]! / knownSum) * knownShare : 1 / n,
  );
  return fit(
    shares.map((p) => p * container),
    mins,
    container,
  );
};

/** Hidden columns may keep at most this much of the width between them, so the visible ones always get a share. */
const MAX_HIDDEN_SHARE = 0.9;

/**
 * The proportions to save after a resize. The visible columns' new fractions
 * are kept relative to each other; a hidden column keeps the fraction it had,
 * so showing it again gives back its old share. Keys of columns the table no
 * longer has (not in `allKeys`) are dropped.
 */
export const mergeProportions = (
  stored: Record<string, unknown> | null | undefined,
  visible: Record<string, number>,
  allKeys: readonly string[],
): Record<string, number> => {
  const all = new Set(allKeys);
  const saved = cleanProportions(stored);
  const savedSum = sum(Object.values(saved));
  const hidden = Object.entries(saved).filter(([k]) => all.has(k) && visible[k] === undefined);
  const rawHidden = savedSum > 0 ? sum(hidden.map(([, v]) => v)) / savedSum : 0;
  const hiddenShare = Math.min(rawHidden, MAX_HIDDEN_SHARE);
  const hiddenScale = rawHidden > 0 ? hiddenShare / rawHidden / savedSum : 0;

  const visibleEntries = Object.entries(visible).filter(([k]) => all.has(k));
  const visibleWeights = weightsOf(visibleEntries.map(([, v]) => v));
  const visibleSum = sum(visibleWeights);

  return Object.fromEntries([
    ...visibleEntries.map(([k], i) => [k, roundProportion((visibleWeights[i]! / visibleSum) * (1 - hiddenShare))]),
    ...hidden.map(([k, v]) => [k, roundProportion(v * hiddenScale)]),
  ]);
};
