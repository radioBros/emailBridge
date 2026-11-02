export interface SortItem {
  key: string;
  order: "asc" | "desc";
}

export interface SortPageOptions {
  /** Keys compared as ISO / Date values. */
  dateKeys?: string[];
  /** Keys compared numerically. */
  numberKeys?: string[];
}

const cmp = (a: unknown, b: unknown, mode: "string" | "number" | "date"): number => {
  if (a == null && b == null) return 0;
  if (a == null) return 1;
  if (b == null) return -1;

  if (mode === "number") {
    return (Number(a) || 0) - (Number(b) || 0);
  }
  if (mode === "date") {
    const ta = new Date(String(a)).getTime();
    const tb = new Date(String(b)).getTime();
    const na = Number.isFinite(ta) ? ta : 0;
    const nb = Number.isFinite(tb) ? tb : 0;
    return na - nb;
  }
  return String(a).localeCompare(String(b), undefined, { sensitivity: "base" });
};

/**
 * Sort then page a full in-memory snapshot (cluster / queues APIs return everything).
 */
export const sortAndPage = <T extends Record<string, unknown>>(
  items: readonly T[],
  sortBy: readonly SortItem[],
  page: number,
  ipp: number,
  opts: SortPageOptions = {},
): { rows: T[]; total: number } => {
  const total = items.length;
  const dateKeys = new Set(opts.dateKeys ?? []);
  const numberKeys = new Set(opts.numberKeys ?? []);
  const sort = sortBy[0];

  const rows = items.slice();
  if (sort?.key) {
    const mode = dateKeys.has(sort.key)
      ? "date"
      : numberKeys.has(sort.key)
        ? "number"
        : "string";
    const dir = sort.order === "desc" ? -1 : 1;
    rows.sort((a, b) => dir * cmp(a[sort.key], b[sort.key], mode));
  }

  const size = Math.max(1, ipp || 1);
  const p = Math.max(1, page || 1);
  const start = (p - 1) * size;
  return { rows: rows.slice(start, start + size), total };
};
