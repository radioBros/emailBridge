/**
 * Settings quick search: accent- and case-insensitive multi-word matching with highlight ranges.
 *
 * Text is normalised one code point at a time (lowercase → NFD → strip combining marks) and every
 * normalised UTF-16 unit remembers the original code point it came from, so a match found in the
 * normalised text maps back to exact code-point ranges in the displayed text ("Fréquence" / "frequ").
 */

/** Half-open code-point range `[start, end)` in the original text. */
export interface MatchRange {
  start: number;
  end: number;
}

export interface HighlightSegment {
  text: string;
  match: boolean;
}

/** The fields a result is matched on, in ranking order (label best, tab name worst). */
export const SEARCH_FIELDS = ["label", "description", "key", "tab"] as const;
export type SearchField = (typeof SEARCH_FIELDS)[number];

export interface SearchEntry {
  /** Unique id (e.g. `field:webhookTimeout`, `switch:sandbox.enabled`, `section:oidc`). */
  id: string;
  label: string;
  description?: string;
  key: string;
  tab: string;
}

export interface SearchResult<T extends SearchEntry = SearchEntry> {
  entry: T;
  /** 0 = a word hit the label, 1 = description, 2 = key, 3 = only the tab name. */
  rank: number;
  ranges: Record<SearchField, MatchRange[]>;
}

const MARKS = /\p{M}/gu;

interface Normalised {
  text: string;
  /** For each UTF-16 unit of `text`, the original code-point index. */
  origin: number[];
}

/** Lowercase + strip diacritics, keeping a map back to original code points. */
export const normaliseWithMap = (input: string): Normalised => {
  const chars = Array.from(input);
  let text = "";
  const origin: number[] = [];
  chars.forEach((ch, i) => {
    const piece = ch.toLowerCase().normalize("NFD").replace(MARKS, "");
    text += piece;
    for (let u = 0; u < piece.length; u++) origin.push(i);
  });
  return { text, origin };
};

export const normalise = (input: string): string => normaliseWithMap(input).text;

/** Query → distinct normalised words (empty words dropped). */
export const tokenize = (query: string): string[] => {
  const words = normalise(query)
    .split(/\s+/)
    .filter(Boolean);
  return [...new Set(words)];
};

/** Sorts and merges overlapping or touching ranges. */
export const mergeRanges = (ranges: MatchRange[]): MatchRange[] => {
  const sorted = [...ranges]
    .filter((r) => r.end > r.start)
    .sort((a, b) => a.start - b.start || a.end - b.end);
  const out: MatchRange[] = [];
  for (const r of sorted) {
    const last = out[out.length - 1];
    if (last && r.start <= last.end) {
      last.end = Math.max(last.end, r.end);
    } else {
      out.push({ ...r });
    }
  }
  return out;
};

/** Every occurrence of every word in `text`, as merged original code-point ranges. */
export const matchRanges = (text: string | undefined, words: string[]): MatchRange[] => {
  if (!text || !words.length) return [];
  const { text: norm, origin } = normaliseWithMap(text);
  const found: MatchRange[] = [];
  for (const word of words) {
    let from = 0;
    for (;;) {
      const at = norm.indexOf(word, from);
      if (at < 0) break;
      const start = origin[at] ?? 0;
      const end = (origin[at + word.length - 1] ?? start) + 1;
      found.push({ start, end });
      from = at + 1;
    }
  }
  return mergeRanges(found);
};

/** Splits `text` into plain and matched segments (code-point ranges, never HTML). */
export const highlightSegments = (text: string, ranges: MatchRange[] = []): HighlightSegment[] => {
  const chars = Array.from(text);
  const out: HighlightSegment[] = [];
  let at = 0;
  for (const r of mergeRanges(ranges)) {
    const start = Math.max(r.start, at);
    const end = Math.min(r.end, chars.length);
    if (end <= start) continue;
    if (start > at) out.push({ text: chars.slice(at, start).join(""), match: false });
    out.push({ text: chars.slice(start, end).join(""), match: true });
    at = end;
  }
  if (at < chars.length) out.push({ text: chars.slice(at).join(""), match: false });
  return out;
};

const fieldText = (entry: SearchEntry, field: SearchField): string | undefined => {
  switch (field) {
    case "label":
      return entry.label;
    case "description":
      return entry.description;
    case "key":
      return entry.key;
    default:
      return entry.tab;
  }
};

/**
 * Filters and orders `entries` for `query`. Every word must occur in at least one of label,
 * description, key or tab name (any order). Results are grouped by the best field any word hit
 * (label, then description, then key, then tab name) and keep catalog order inside a group.
 */
export const searchEntries = <T extends SearchEntry>(
  entries: T[],
  query: string,
): SearchResult<T>[] => {
  const words = tokenize(query);
  if (!words.length) return [];
  const results: Array<SearchResult<T> & { order: number }> = [];
  entries.forEach((entry, order) => {
    const norms = SEARCH_FIELDS.map((f) => normalise(fieldText(entry, f) ?? ""));
    const allMatch = words.every((w) => norms.some((n) => n.includes(w)));
    if (!allMatch) return;
    const rank = norms.findIndex((n) => words.some((w) => n.includes(w)));
    const ranges = {} as Record<SearchField, MatchRange[]>;
    for (const f of SEARCH_FIELDS) ranges[f] = matchRanges(fieldText(entry, f), words);
    results.push({ entry, rank, ranges, order });
  });
  results.sort((a, b) => a.rank - b.rank || a.order - b.order);
  return results.map(({ entry, rank, ranges }) => ({ entry, rank, ranges }));
};
