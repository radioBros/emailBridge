/** Lightweight CEL token highlighter — no third-party deps (brief 19). */

export type CelTokenKind =
  | "keyword"
  | "function"
  | "string"
  | "number"
  | "operator"
  | "comment"
  | "ident"
  | "punct"
  | "plain";

export interface CelToken {
  kind: CelTokenKind;
  text: string;
}

const KEYWORDS = new Set([
  "true",
  "false",
  "null",
  "in",
  "has",
  "all",
  "exists",
  "exists_one",
  "map",
  "filter",
]);

const FUNCTIONS = new Set([
  "header",
  "headerValues",
  "hasHeader",
  "domain",
  "addresses",
  "size",
  "int",
  "uint",
  "double",
  "string",
  "type",
  "timestamp",
  "duration",
  "matches",
  "startsWith",
  "endsWith",
  "contains",
]);

const isIdentStart = (c: string): boolean => /[A-Za-z_]/.test(c);
const isIdent = (c: string): boolean => /[A-Za-z0-9_]/.test(c);

/** Tokenise CEL source for display highlighting. */
export const tokenizeCel = (source: string): CelToken[] => {
  const out: CelToken[] = [];
  let i = 0;
  const push = (kind: CelTokenKind, text: string): void => {
    if (text) out.push({ kind, text });
  };

  while (i < source.length) {
    const c = source[i]!;

    if (c === "/" && source[i + 1] === "/") {
      const start = i;
      i += 2;
      while (i < source.length && source[i] !== "\n") i++;
      push("comment", source.slice(start, i));
      continue;
    }

    if (c === '"' || c === "'") {
      const quote = c;
      const start = i;
      i++;
      while (i < source.length) {
        if (source[i] === "\\") {
          i += 2;
          continue;
        }
        if (source[i] === quote) {
          i++;
          break;
        }
        i++;
      }
      push("string", source.slice(start, i));
      continue;
    }

    if (/[0-9]/.test(c)) {
      const start = i;
      while (i < source.length && /[0-9.]/.test(source[i]!)) i++;
      push("number", source.slice(start, i));
      continue;
    }

    if (isIdentStart(c)) {
      const start = i;
      i++;
      while (i < source.length && isIdent(source[i]!)) i++;
      const text = source.slice(start, i);
      const next = source[i];
      if (KEYWORDS.has(text)) push("keyword", text);
      else if (FUNCTIONS.has(text) || next === "(") push("function", text);
      else push("ident", text);
      continue;
    }

    if ("=!<>&|+-*/%?:.".includes(c)) {
      const start = i;
      i++;
      while (i < source.length && "=<>&|.".includes(source[i]!)) i++;
      push("operator", source.slice(start, i));
      continue;
    }

    if ("()[]{},;".includes(c)) {
      push("punct", c);
      i++;
      continue;
    }

    push("plain", c);
    i++;
  }

  return out;
};

/** Parse `line:col` / `at line N column M` style positions from a CEL error string. */
export const parseCelErrorPosition = (
  error: string | null | undefined,
): { line?: number; column?: number; message: string } => {
  const message = (error ?? "").trim();
  if (!message) return { message: "" };
  const m1 = /(?:line|L)\s*[:=]?\s*(\d+)\s*[, ]\s*(?:col(?:umn)?|C)\s*[:=]?\s*(\d+)/i.exec(message);
  if (m1) return { line: Number(m1[1]), column: Number(m1[2]), message };
  const m2 = /(\d+)\s*:\s*(\d+)/.exec(message);
  if (m2) return { line: Number(m2[1]), column: Number(m2[2]), message };
  return { message };
};

export const CEL_MAX_BYTES = 4096;
