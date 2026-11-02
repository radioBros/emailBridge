// =========================================================================
// useColumnResize — user-resizable column widths that always fill the table.
//
// Until the user resizes a column the table keeps Vuetify's automatic layout
// (already full width). The first resize measures the header cells and from
// then on every column has a px width and the table uses
// `table-layout: fixed` at exactly its container's width:
//
//   • dragging column N's right border changes N and the columns to its right
//     absorb the opposite change — the immediate neighbour first, cascading to
//     the next one only when it reaches its minimum; when every column to the
//     right is at its minimum, the drag stops (helpers/ColumnWidths.resize);
//   • fixed columns (actions, `data-table-*`, `resizable: false`) keep their
//     content width and are never resized or used to absorb a resize;
//   • container changes (window, drawer, ColManager hiding / showing /
//     reordering) rescale the columns proportionally; a newly shown column gets
//     its default share. Only when the minimums do not fit does the table
//     scroll sideways.
//
// Widths persist per user through the settings store (`tables.<name>.widths`)
// as PROPORTIONS of the resizable width, so a layout saved on one screen fills
// another. A drag updates a local px draft only; the store (and so the PATCH)
// is written once, when the drag ends or a keyboard burst ends.
//
// The owner (ServerTable) keeps `container` (the scroll box's inner width) and
// `fixedWidths` (content width of each fixed column, see measureContentWidth)
// current; ColResizer drives begin / drag / step / reset / commit / cancel.
// =========================================================================

import { computed, ref, toValue, type MaybeRefOrGetter } from 'vue';
import { useSettingsStore } from '@/stores/settings';
import {
  MIN_COLUMN_WIDTH,
  cleanProportions,
  fit,
  fromProportions,
  mergeProportions,
  resize as resizeWidths,
  resizeRange,
  toProportions,
} from '@/helpers/ColumnWidths';

export { MIN_COLUMN_WIDTH };

/** One visible column, in display order. */
export interface ResizeColumn {
  key: string;
  /** Narrowest width in px (helpers/ColumnWidths.columnMin). */
  min: number;
  /** Fixed column: content-sized, never resized, never absorbs a resize. */
  fixed: boolean;
  /** A fixed column's declared width in px, used instead of measuring it. */
  width?: number;
}

const sum = (values: readonly number[]): number => values.reduce((s, v) => s + v, 0);

/**
 * The width a table cell needs to show its content on one line: its content
 * laid out at max-content in a hidden copy inside the cell (so the cell's
 * styles still apply), plus the cell's padding and borders. Measured in one
 * synchronous pass, so the copy is never painted.
 */
export const measureContentWidth = (cell: HTMLElement): number => {
  const probe = document.createElement('div');
  probe.setAttribute('aria-hidden', 'true');
  probe.style.cssText =
    'position:absolute;left:0;top:0;visibility:hidden;pointer-events:none;width:max-content;white-space:nowrap;';
  cell.childNodes.forEach((node) => probe.appendChild(node.cloneNode(true)));
  cell.appendChild(probe);
  const content = probe.getBoundingClientRect().width;
  probe.remove();
  const cs = getComputedStyle(cell);
  const px = (v: string): number => Number.parseFloat(v) || 0;
  return Math.ceil(
    content + px(cs.paddingLeft) + px(cs.paddingRight) + px(cs.borderLeftWidth) + px(cs.borderRightWidth),
  );
};

export const useColumnResize = (
  tableName: MaybeRefOrGetter<string>,
  columns: MaybeRefOrGetter<readonly ResizeColumn[]>,
  allKeys: MaybeRefOrGetter<readonly string[]>,
) => {
  const settings = useSettingsStore();

  /** Inner width of the table's scroll box in px (without its scrollbar). Kept current by the table. */
  const container = ref(0);
  /** Content width in px of each fixed column, by key. Kept current by the table. */
  const fixedWidths = ref<Record<string, number>>({});

  const cols = computed(() => toValue(columns));
  const resizable = computed(() => cols.value.filter((c) => !c.fixed));
  const keys = computed(() => resizable.value.map((c) => c.key));
  const mins = computed(() => resizable.value.map((c) => c.min));
  const fixedCols = computed(() => cols.value.filter((c) => c.fixed));

  const fixedWidth = (c: ResizeColumn): number | undefined => c.width ?? fixedWidths.value[c.key];
  const fixedKnown = computed(() => fixedCols.value.every((c) => fixedWidth(c) !== undefined));
  const fixedTotal = computed(() => sum(fixedCols.value.map((c) => fixedWidth(c) ?? 0)));

  /** Width the resizable columns share. */
  const available = computed(() => Math.max(0, container.value - fixedTotal.value));

  const stored = computed(() => settings.getTable(toValue(tableName))?.widths);
  const hasSaved = computed(() => Object.keys(cleanProportions(stored.value)).length > 0);

  // Px widths of the resizable columns while the user resizes; null when idle.
  const draft = ref<number[] | null>(null);
  // The widths when the current drag began: a drag is always applied to these.
  let start: number[] = [];

  const draftValid = computed(() => draft.value !== null && draft.value.length === keys.value.length);

  /** Whether the table can be laid out from widths: its box and its fixed columns are measured. */
  const ready = computed(() => container.value > 0 && fixedKnown.value && keys.value.length > 0);

  /** Whether every column has a px width (else Vuetify's automatic layout). */
  const sized = computed(() => draftValid.value || (hasSaved.value && ready.value));

  /** Px widths of the resizable columns, aligned with `keys`. */
  const layout = computed<number[]>(() =>
    draftValid.value ? draft.value! : fromProportions(keys.value, stored.value, mins.value, available.value),
  );

  /** Px width of every visible column once the table is sized, else empty. */
  const widths = computed<Record<string, number>>(() => {
    if (!sized.value) return {};
    const out: Record<string, number> = {};
    keys.value.forEach((k, i) => {
      out[k] = layout.value[i]!;
    });
    for (const c of fixedCols.value) out[c.key] = fixedWidth(c) ?? 0;
    return out;
  });

  /** Width in px of a column once the table is sized, else undefined (automatic). */
  const width = (key: string): number | undefined => widths.value[key];

  /** Sum of the visible columns' widths: the table's own width (0 when automatic). */
  const totalWidth = computed(() => sum(Object.values(widths.value)));

  /** Whether a column has a resize handle: resizable, with a resizable column somewhere to its right. */
  const canResize = (key: string): boolean => {
    const i = keys.value.indexOf(key);
    return i >= 0 && i < keys.value.length - 1;
  };

  /** The column's width and the range a resize can take it to (for the handle's ARIA values). */
  const range = (key: string): { now?: number; min: number; max?: number } => {
    const i = keys.value.indexOf(key);
    const min = mins.value[i] ?? MIN_COLUMN_WIDTH;
    if (i < 0 || !sized.value) return { min };
    const r = resizeRange(layout.value, mins.value, i);
    return { now: layout.value[i], min: r.min, max: r.max };
  };

  // Widths of the header row's cells (auto layout), aligned with `cols`.
  const measureRow = (th: HTMLElement): void => {
    const cells = th.parentElement ? Array.from(th.parentElement.children) : [];
    const aligned = cells.length === cols.value.length;
    const fixed: Record<string, number> = { ...fixedWidths.value };
    cols.value.forEach((c, i) => {
      if (c.fixed && fixedWidth(c) === undefined) {
        fixed[c.key] = aligned ? Math.ceil(cells[i]!.getBoundingClientRect().width) : MIN_COLUMN_WIDTH;
      }
    });
    fixedWidths.value = fixed;
    const measured = cols.value
      .map((c, i) => (c.fixed ? null : aligned ? cells[i]!.getBoundingClientRect().width : 1))
      .filter((w): w is number => w !== null);
    draft.value = fit(measured, mins.value, available.value);
  };

  /**
   * Starts a resize of `key` from its header cell. The first resize ever
   * freezes every column at its rendered width (fitted to the container), so
   * nothing jumps. Returns false when the column cannot be resized.
   */
  const begin = (key: string, th: HTMLElement): boolean => {
    if (!canResize(key)) return false;
    if (container.value <= 0) {
      container.value = th.closest('.v-table__wrapper')?.clientWidth ?? 0;
    }
    if (!draftValid.value) {
      if (sized.value) draft.value = [...layout.value];
      else measureRow(th);
    }
    start = [...draft.value!];
    return true;
  };

  /** A drag of `key`'s right border by `delta` px from where it began (not saved yet). */
  const drag = (key: string, delta: number): void => {
    const i = keys.value.indexOf(key);
    if (!draftValid.value || i < 0 || start.length !== keys.value.length) return;
    draft.value = resizeWidths(start, mins.value, i, delta);
  };

  /** One keyboard step of `delta` px (not saved yet: commit when the key is released). */
  const step = (key: string, delta: number, th: HTMLElement): void => {
    if (!begin(key, th)) return;
    drag(key, delta);
  };

  /** Ends a resize: saves the widths as proportions. */
  const commit = (): void => {
    if (!draftValid.value) {
      draft.value = null;
      return;
    }
    const merged = mergeProportions(stored.value, toProportions(keys.value, draft.value!), toValue(allKeys));
    settings.setWidths(toValue(tableName), merged);
    draft.value = null;
  };

  /** Drops an unsaved resize. */
  const cancel = (): void => {
    draft.value = null;
  };

  /** Resets a column to its default share (1/n of the resizable width), with the usual neighbour rules, and saves. */
  const reset = (key: string, th: HTMLElement): void => {
    if (!begin(key, th)) return;
    const i = keys.value.indexOf(key);
    const target = Math.max(mins.value[i] ?? MIN_COLUMN_WIDTH, Math.round(available.value / keys.value.length));
    drag(key, target - start[i]!);
    commit();
  };

  return {
    container,
    fixedWidths,
    widths,
    sized,
    width,
    totalWidth,
    canResize,
    range,
    begin,
    drag,
    step,
    commit,
    cancel,
    reset,
  };
};

export type ColumnResize = ReturnType<typeof useColumnResize>;
