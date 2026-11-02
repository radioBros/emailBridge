import type { DisplayBreakpoint } from 'vuetify';
import type { CardGrid, CardGridColumns } from '@/components/tables/types';

/**
 * Pure logic behind CardList: which grid applies, how cards group into virtual rows, and how server
 * pages accumulate into one infinite list.
 */

const BREAKPOINTS: readonly DisplayBreakpoint[] = ['xs', 'sm', 'md', 'lg', 'xl', 'xxl'];

/** Cards per row at `current`: the value of the largest breakpoint at or below it (mobile first). */
export const resolveGridColumns = (grid: CardGrid, current: DisplayBreakpoint): CardGridColumns => {
  const upTo = BREAKPOINTS.indexOf(current);
  for (let i = upTo; i >= 0; i--) {
    const columns = grid[BREAKPOINTS[i]];
    if (columns) return columns;
  }
  return 1;
};

export interface CardRow<T> {
  key: string;
  items: T[];
}

/**
 * Groups cards into rows of `size`. The virtual list measures ROWS, so a row of N cards must be one
 * entry: a card per entry would make the scroller stack cells that the grid lays out side by side.
 */
export const chunkRows = <T>(items: readonly T[], size: number, keyOf: (item: T) => unknown): CardRow<T>[] => {
  const rows: CardRow<T>[] = [];
  for (let i = 0; i < items.length; i += size) {
    const slice = items.slice(i, i + size);
    rows.push({ key: slice.map((item) => String(keyOf(item))).join('|'), items: slice });
  }
  return rows;
};

/**
 * Accumulates a server page into the loaded list. Page `page` replaces the slots it owns, and
 * everything AFTER it is dropped: those later pages were fetched against an older state of the list.
 * Page 1 therefore starts over. A record that moved between pages keeps only its fresh copy.
 */
export const mergePage = <T>(
  loaded: readonly T[],
  incoming: readonly T[],
  page: number,
  ipp: number,
  keyOf: (item: T) => unknown,
): T[] => {
  const fresh = new Set(incoming.map(keyOf));
  const before = loaded.slice(0, Math.max(0, (page - 1) * ipp)).filter((item) => !fresh.has(keyOf(item)));
  return [...before, ...incoming];
};
