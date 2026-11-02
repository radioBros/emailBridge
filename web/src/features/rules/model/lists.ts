import type { RuleListKind } from "@/api/rules";

/** Named list limits (W73 §2.6). */
export const LIST_LIMITS = { entries: 5000, entryBytes: 256 };

/**
 * Pasted text → list entries: one per line (for domains / addresses commas and semicolons also split), trimmed, lower-cased for
 * domains and addresses, a leading "@" dropped from domains, duplicates removed.
 */
export const parseEntries = (
  text: string,
  kind: RuleListKind,
): { entries: string[]; duplicates: number; tooLong: string[] } => {
  const seen = new Set<string>();
  const entries: string[] = [];
  let duplicates = 0;
  const tooLong: string[] = [];
  for (const raw of text.split(kind === "keywords" ? /\n+/ : /[\n,;]+/)) {
    let v = raw.trim();
    if (!v) continue;
    if (kind !== "keywords") v = v.toLowerCase();
    if (kind === "domains") v = v.replace(/^@/, "");
    if (seen.has(v)) {
      duplicates += 1;
      continue;
    }
    seen.add(v);
    if (new TextEncoder().encode(v).length > LIST_LIMITS.entryBytes) tooLong.push(v);
    entries.push(v);
  }
  return { entries, duplicates, tooLong };
};
