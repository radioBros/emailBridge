import { parseMap, toCel } from "@/features/rules/model/mapTree";

/** Code-mode helpers: formatting, snippets, error positions and payload needs. */

export interface Snippet {
  id: string;
  target: "filter" | "map";
  code: string;
}

export const SNIPPETS: Snippet[] = [
  { id: "inboxOnly", target: "filter", code: 'type == "message.received" && isInbox()' },
  { id: "fromDomain", target: "filter", code: 'type == "message.received" && fromDomain() == "example.com"' },
  { id: "pdfAttachment", target: "filter", code: 'type == "message.received" && hasAttachment("*.pdf")' },
  { id: "businessHours", target: "filter", code: 'businessHours("Europe/Rome", "09:00-18:00", "Mon-Fri")' },
  { id: "namedList", target: "filter", code: 'inList("vip", fromDomain())' },
  { id: "noAutoReply", target: "filter", code: 'type == "message.received" && !isAutoReply() && !isBounce()' },
  {
    id: "outboundStatus",
    target: "filter",
    code: 'type in ["message.sent", "message.failed", "message.delivery_attempt_failed"]',
  },
  {
    id: "keyedInbound",
    target: "map",
    code:
      '{\n  "key": env.webhookKey,\n  "body": {\n    "id": data.messageId,\n    "date": event.time,\n' +
      '    "account": account.externalId != "" ? account.externalId : account.id,\n    "event": "messageNew",\n' +
      '    "headers": data.headers,\n    "emailid": data.messageIdHeader,\n    "subject": data.subject\n  }\n}',
  },
  { id: "keyedStatus", target: "map", code: '{\n  "key": env.webhookKey,\n  "body": event\n}' },
  { id: "pickFields", target: "map", code: 'pick(data, ["messageId", "subject", "from", "date"])' },
  { id: "mergeExtra", target: "map", code: 'merge(event, {"vip": inList("vip", fromDomain())})' },
];

/** Splits at top-level `&&` / `||` (outside brackets and strings). */
export const splitTopLevel = (src: string): { parts: string[]; ops: string[] } => {
  const parts: string[] = [];
  const ops: string[] = [];
  let depth = 0;
  let quote: string | null = null;
  let start = 0;
  for (let i = 0; i < src.length; i += 1) {
    const c = src[i]!;
    if (quote) {
      if (c === "\\") i += 1;
      else if (c === quote) quote = null;
      continue;
    }
    if (c === '"' || c === "'") quote = c;
    else if ("([{".includes(c)) depth += 1;
    else if (")]}".includes(c)) depth -= 1;
    else if (depth === 0 && (src.startsWith("&&", i) || src.startsWith("||", i))) {
      parts.push(src.slice(start, i).trim());
      ops.push(src.slice(i, i + 2));
      start = i + 2;
      i += 1;
    }
  }
  parts.push(src.slice(start).trim());
  return { parts, ops };
};

/** Formats a filter (one top-level term per line) or a map (the tree layout). */
export const formatCel = (src: string, target: "filter" | "map"): string => {
  const text = src.trim();
  if (!text) return "";
  if (target === "map") {
    const parsed = parseMap(text);
    return parsed.ok && parsed.node ? toCel(parsed.node) : text;
  }
  const { parts, ops } = splitTopLevel(text.replace(/\s*\n\s*/g, " "));
  if (parts.length < 2) return parts[0] ?? text;
  return parts.map((p, i) => (i === 0 ? p : `${ops[i - 1]} ${p}`)).join("\n");
};

export interface CodeError {
  line: number;
  column: number;
  message: string;
}

/** 1-based line/column → 0-based offset, clamped to the text. */
export const offsetOf = (text: string, line: number, column: number): number => {
  const lines = text.split("\n");
  const l = Math.min(Math.max(line, 1), lines.length);
  let off = 0;
  for (let i = 0; i < l - 1; i += 1) off += lines[i]!.length + 1;
  return Math.min(off + Math.max(column, 1) - 1, text.length);
};

/** Position from a cel-go message (`<input>:2:5: …`), for errors without `errorAt`. */
export const positionInMessage = (message: string): { line: number; column: number } | null => {
  const m = /<input>:(\d+):(\d+)/.exec(message) ?? /line (\d+),? col(?:umn)? (\d+)/i.exec(message);
  return m ? { line: Number(m[1]), column: Number(m[2]) } : null;
};

export type PayloadNeed = "text" | "attachments" | "headers";

/**
 * What a rule makes the sync fetch for every message (W73 §3), so the editor can warn that body or
 * attachment filters cost I/O. Mirrors `rules.NeedsFromExpr`.
 */
export const payloadNeeds = (...sources: string[]): PayloadNeed[] => {
  const src = sources.join("\n");
  const out = new Set<PayloadNeed>();
  if (/\b(text|textContains|textMatches)\s*\(|\bdata\.text\b/.test(src)) out.add("text");
  if (/\b(hasAttachments|attachments|hasAttachment|attachmentTypes)\s*\(|\bdata\.attachments\b/.test(src)) {
    out.add("attachments");
  }
  if (/\bdata\.headers\b(?!\s*[.[])/.test(src)) out.add("headers");
  return [...out];
};

export interface DiffLine {
  t: "same" | "add" | "del";
  s: string;
}

/** Line diff (LCS) from `before` to `after`, for the version history. */
export const lineDiff = (before: string, after: string): DiffLine[] => {
  const a = before ? before.split("\n") : [];
  const b = after ? after.split("\n") : [];
  const n = a.length;
  const m = b.length;
  const lcs: number[][] = Array.from({ length: n + 1 }, () => new Array<number>(m + 1).fill(0));
  for (let i = n - 1; i >= 0; i -= 1) {
    for (let j = m - 1; j >= 0; j -= 1) {
      lcs[i]![j] = a[i] === b[j] ? lcs[i + 1]![j + 1]! + 1 : Math.max(lcs[i + 1]![j]!, lcs[i]![j + 1]!);
    }
  }
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[i] === b[j]) {
      out.push({ t: "same", s: a[i]! });
      i += 1;
      j += 1;
    } else if (lcs[i + 1]![j]! >= lcs[i]![j + 1]!) {
      out.push({ t: "del", s: a[i]! });
      i += 1;
    } else {
      out.push({ t: "add", s: b[j]! });
      j += 1;
    }
  }
  while (i < n) out.push({ t: "del", s: a[i++]! });
  while (j < m) out.push({ t: "add", s: b[j++]! });
  return out;
};
