import type { SearchHighlight } from "@/features/search/helpers/api";

export interface SnippetSegment {
  text: string;
  match: boolean;
}

/**
 * Splits a snippet into plain and matched segments. Highlights are code-point ranges (the API counts
 * Unicode code points, not UTF-16 units), so the text is split with Array.from. Out-of-range, overlapping
 * or unordered ranges are ignored rather than trusted.
 */
export const snippetSegments = (text: string, highlights: SearchHighlight[] = []): SnippetSegment[] => {
  const chars = Array.from(text);
  const out: SnippetSegment[] = [];
  let at = 0;
  const ranges = [...highlights].sort((a, b) => a.start - b.start);
  for (const h of ranges) {
    if (!Number.isInteger(h.start) || !Number.isInteger(h.end)) continue;
    if (h.start < at || h.end <= h.start || h.end > chars.length) continue;
    if (h.start > at) out.push({ text: chars.slice(at, h.start).join(""), match: false });
    out.push({ text: chars.slice(h.start, h.end).join(""), match: true });
    at = h.end;
  }
  if (at < chars.length) out.push({ text: chars.slice(at).join(""), match: false });
  return out;
};
