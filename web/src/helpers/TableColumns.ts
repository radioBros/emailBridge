/**
 * The invariants every ServerTable column layout must keep, enforced at RENDER time rather than
 * trusted to every path that writes order or visibility:
 *
 * - the actions column is always LAST, always visible, never sortable;
 * - a FORCED column is always visible (ColManager disables its checkbox, so a saved layout that hid
 *   it would make it impossible to show again).
 *
 * Saved layouts (per `tableName`) and dynamic header sets (a parent swapping columns per tab under
 * one tableName) could otherwise produce both broken states.
 */

export const ACTION_KEYS: ReadonlySet<string> = new Set(['_actions', 'tableActions', 'actions']);

export const isActionKey = (key: string): boolean => ACTION_KEYS.has(key);

export interface ColumnHeader {
  key: string;
  forced?: boolean;
  sortable?: boolean;
  [key: string]: unknown;
}

/** Actions columns are forced and never sortable, whatever the caller declared. */
export const normalizeHeaders = <H extends ColumnHeader>(headers: readonly H[]): H[] =>
  headers.map((h) => (isActionKey(h.key) ? { ...h, forced: true, sortable: false } : h));

/**
 * The columns to render, in order. `ordered` supplies the ORDER only; the definitions come from
 * `headers` (the current set), so a column that no longer exists is dropped and one missing from
 * `ordered` is appended BEFORE the actions column, never after.
 */
export const arrangeColumns = <H extends ColumnHeader>(
  headers: readonly H[],
  ordered: readonly { key: string }[],
  visibleKeys: readonly string[],
): H[] => {
  const byKey = new Map(headers.map((h) => [h.key, h]));
  const seen = new Set<string>();
  const body: H[] = [];
  for (const { key } of [...ordered, ...headers]) {
    if (seen.has(key) || isActionKey(key)) continue;
    const h = byKey.get(key);
    if (!h) continue;
    seen.add(key);
    body.push(h);
  }
  const visible = new Set(visibleKeys);
  return [...body.filter((h) => h.forced === true || visible.has(h.key)), ...headers.filter((h) => isActionKey(h.key))];
};

/**
 * A saved column ORDER, healed against the current header keys.
 *
 * A saved order can miss keys (a column added in a later release, or one hidden when the layout was
 * first saved). Appending such a key at the end is what made a re-shown column jump to the far
 * right; instead it is re-inserted right after the nearest column that precedes it in the default
 * order. Keys that no longer exist are dropped; `null` (nothing saved) is the default order.
 */
export const mergeColumnOrder = (saved: readonly string[] | null | undefined, defaults: readonly string[]): string[] => {
  const current = new Set(defaults);
  const out = (saved ?? []).filter((k, i, a) => current.has(k) && a.indexOf(k) === i);
  defaults.forEach((key, idx) => {
    if (out.includes(key)) return;
    let at = 0; // no preceding default column placed yet: goes first
    for (let j = idx - 1; j >= 0; j--) {
      const pos = out.indexOf(defaults[j]);
      if (pos !== -1) {
        at = pos + 1;
        break;
      }
    }
    out.splice(at, 0, key);
  });
  return out;
};
