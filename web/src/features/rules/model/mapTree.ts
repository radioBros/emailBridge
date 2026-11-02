/**
 * The visual payload editor's model and its round trip with CEL text.
 *
 * A map expression is shown as a tree when it is an object / list literal (or a single value); every value
 * that is not a literal, a field path, a secret or a no-argument helper stays an `expr` leaf holding its
 * source, so ANY map parses (only comments and non-string keys force code mode). `toCel(parse(x))` is
 * stable: generating, parsing and generating again gives the same text.
 */

export type MapKind = "object" | "list" | "field" | "constant" | "secret" | "helper" | "expr";

export interface MapEntry {
  key: string;
  node: MapNode;
}

export type MapNode =
  | { kind: "object"; entries: MapEntry[] }
  | { kind: "list"; items: MapNode[] }
  | { kind: "field"; path: string }
  | { kind: "constant"; value: string | number | boolean | null }
  | { kind: "secret"; name: string }
  | { kind: "helper"; call: string }
  | { kind: "expr"; source: string };

/** No-argument helpers that make sense as a value. */
export const VALUE_HELPERS = [
  "from",
  "fromDomain",
  "replyTo",
  "subject",
  "text",
  "labels",
  "toAddresses",
  "ccAddresses",
  "attachments",
  "attachmentTypes",
  "hasAttachments",
  "isInbox",
  "isReply",
  "isForward",
  "isAutoReply",
  "isBounce",
  "spf",
  "dkim",
  "dmarc",
  "spamScore",
  "pecType",
  "isPecReceipt",
];

const PATH_RE = /^(event|data|account)(\.[A-Za-z_][A-Za-z0-9_]*)*$|^type$/;
const SECRET_RE = /^env\.([A-Za-z_][A-Za-z0-9_]{0,63})$|^env\[\s*"([A-Za-z_][A-Za-z0-9_]{0,63})"\s*\]$/;
const HELPER_RE = /^([A-Za-z_][A-Za-z0-9_]*)\(\s*\)$/;
const NUMBER_RE = /^-?(?:\d+\.\d+|\d+)(?:[eE][+-]?\d+)?$/;
export const SECRET_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;

// ---- generation -------------------------------------------------------------------------------

const quote = (s: string): string => JSON.stringify(s);

const constantToCel = (v: string | number | boolean | null): string => {
  if (v === null) return "null";
  if (typeof v === "string") return quote(v);
  if (typeof v === "number") return Number.isFinite(v) ? String(v) : "0";
  return v ? "true" : "false";
};

const isScalar = (n: MapNode): boolean => n.kind !== "object" && n.kind !== "list";

export const toCel = (node: MapNode | null, indent = 0): string => {
  if (!node) return "";
  const pad = (n: number): string => "  ".repeat(n);
  switch (node.kind) {
    case "object": {
      if (!node.entries.length) return "{}";
      const lines = node.entries.map((e) => `${pad(indent + 1)}${quote(e.key)}: ${toCel(e.node, indent + 1)}`);
      return `{\n${lines.join(",\n")}\n${pad(indent)}}`;
    }
    case "list": {
      if (!node.items.length) return "[]";
      if (node.items.every(isScalar)) return `[${node.items.map((i) => toCel(i, indent)).join(", ")}]`;
      const lines = node.items.map((i) => `${pad(indent + 1)}${toCel(i, indent + 1)}`);
      return `[\n${lines.join(",\n")}\n${pad(indent)}]`;
    }
    case "field":
      return node.path;
    case "constant":
      return constantToCel(node.value);
    case "secret":
      return `env.${node.name}`;
    case "helper":
      return node.call.endsWith(")") ? node.call : `${node.call}()`;
    case "expr":
      return node.source.trim() || "null";
  }
};

// ---- parsing ----------------------------------------------------------------------------------

interface Token {
  t: "str" | "punct" | "other";
  v: string;
  start: number;
  end: number;
}

/** Splits CEL into strings, brackets / commas / colons and everything else. Null on a comment. */
const tokenize = (src: string): Token[] | null => {
  const out: Token[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i]!;
    if (c === "/" && src[i + 1] === "/") return null;
    if (c === '"' || c === "'") {
      const start = i;
      const triple = src.startsWith(c.repeat(3), i);
      const close = triple ? c.repeat(3) : c;
      i += close.length;
      while (i < src.length && !src.startsWith(close, i)) {
        if (src[i] === "\\") i += 1;
        i += 1;
      }
      if (i >= src.length) return null;
      i += close.length;
      out.push({ t: "str", v: src.slice(start, i), start, end: i });
      continue;
    }
    if ("{}[](),:".includes(c)) {
      out.push({ t: "punct", v: c, start: i, end: i + 1 });
      i += 1;
      continue;
    }
    const start = i;
    while (i < src.length && !"{}[](),:\"'".includes(src[i]!) && !(src[i] === "/" && src[i + 1] === "/")) i += 1;
    if (src.slice(start, i).trim()) out.push({ t: "other", v: src.slice(start, i), start, end: i });
  }
  return out;
};

/** Decodes a plain CEL string literal ("…" / '…'); null for raw / bytes / triple-quoted ones. */
const decodeString = (lit: string): string | null => {
  if (lit.length < 2) return null;
  const q = lit[0];
  if ((q !== '"' && q !== "'") || lit[lit.length - 1] !== q || lit.startsWith(q.repeat(3))) return null;
  const body = lit.slice(1, -1);
  if (q === '"') {
    try {
      return JSON.parse(lit) as string;
    } catch {
      return null;
    }
  }
  try {
    return JSON.parse(`"${body.replace(/\\'/g, "'").replace(/"/g, '\\"')}"`) as string;
  } catch {
    return null;
  }
};

/** Classifies one value's source text. */
export const classifyLeaf = (source: string): MapNode => {
  const s = source.trim();
  if (s === "null") return { kind: "constant", value: null };
  if (s === "true" || s === "false") return { kind: "constant", value: s === "true" };
  if (NUMBER_RE.test(s)) return { kind: "constant", value: Number(s) };
  const str = decodeString(s);
  if (str !== null && tokenize(s)?.length === 1) return { kind: "constant", value: str };
  const secret = SECRET_RE.exec(s);
  if (secret) return { kind: "secret", name: secret[1] ?? secret[2]! };
  if (PATH_RE.test(s)) return { kind: "field", path: s };
  const helper = HELPER_RE.exec(s);
  if (helper && VALUE_HELPERS.includes(helper[1]!)) return { kind: "helper", call: helper[1]! };
  return { kind: "expr", source: s };
};

class Parser {
  i = 0;
  constructor(
    private readonly src: string,
    private readonly toks: Token[],
  ) {}

  peek = (): Token | undefined => this.toks[this.i];

  /** Index of the token that ends the value starting at `from` (a top-level `,` / closer, or the end). */
  private valueEnd = (from: number): number => {
    let depth = 0;
    for (let j = from; j < this.toks.length; j += 1) {
      const tk = this.toks[j]!;
      if (tk.t !== "punct") continue;
      if ("{[(".includes(tk.v)) depth += 1;
      else if ("}])".includes(tk.v)) {
        if (depth === 0) return j;
        depth -= 1;
      } else if (tk.v === "," && depth === 0) return j;
    }
    return this.toks.length;
  };

  /** Parses the value at the cursor; null when it cannot be represented. */
  value = (): MapNode | null => {
    const first = this.peek();
    if (!first) return null;
    const end = this.valueEnd(this.i);
    if (first.t === "punct" && (first.v === "{" || first.v === "[")) {
      // A literal only when the bracket group is the whole value: `{…}.x` or `[…][0]` stay expressions.
      const close = this.matching(this.i);
      if (close === end - 1) return first.v === "{" ? this.object() : this.list();
    }
    const start = first.start;
    const stop = end < this.toks.length ? this.toks[end]!.start : this.src.length;
    this.i = end;
    const text = this.src.slice(start, stop).trim();
    return text ? classifyLeaf(text) : null;
  };

  private matching = (open: number): number => {
    let depth = 0;
    for (let j = open; j < this.toks.length; j += 1) {
      const tk = this.toks[j]!;
      if (tk.t !== "punct") continue;
      if ("{[(".includes(tk.v)) depth += 1;
      else if ("}])".includes(tk.v)) {
        depth -= 1;
        if (depth === 0) return j;
      }
    }
    return -1;
  };

  private object = (): MapNode | null => {
    this.i += 1; // {
    const entries: MapEntry[] = [];
    while (this.peek() && !(this.peek()!.t === "punct" && this.peek()!.v === "}")) {
      const keyTok = this.peek()!;
      const key = keyTok.t === "str" ? decodeString(keyTok.v) : null;
      if (key === null) return null; // only string keys are representable
      this.i += 1;
      const colon = this.peek();
      if (!colon || colon.t !== "punct" || colon.v !== ":") return null;
      this.i += 1;
      const node = this.value();
      if (!node) return null;
      entries.push({ key, node });
      const sep = this.peek();
      if (sep?.t === "punct" && sep.v === ",") this.i += 1;
      else if (!(sep?.t === "punct" && sep.v === "}")) return null;
    }
    if (!this.peek()) return null;
    this.i += 1; // }
    return { kind: "object", entries };
  };

  private list = (): MapNode | null => {
    this.i += 1; // [
    const items: MapNode[] = [];
    while (this.peek() && !(this.peek()!.t === "punct" && this.peek()!.v === "]")) {
      const node = this.value();
      if (!node) return null;
      items.push(node);
      const sep = this.peek();
      if (sep?.t === "punct" && sep.v === ",") this.i += 1;
      else if (!(sep?.t === "punct" && sep.v === "]")) return null;
    }
    if (!this.peek()) return null;
    this.i += 1; // ]
    return { kind: "list", items };
  };
}

export type ParseResult =
  | { ok: true; node: MapNode | null }
  | { ok: false; reason: "comment" | "syntax" };

/** Parses a map expression into a tree. An empty expression is `{ ok: true, node: null }` (no map). */
export const parseMap = (src: string): ParseResult => {
  if (!src.trim()) return { ok: true, node: null };
  const toks = tokenize(src);
  if (!toks) return { ok: false, reason: src.includes("//") ? "comment" : "syntax" };
  let depth = 0;
  for (const tk of toks) {
    if (tk.t !== "punct") continue;
    if ("{[(".includes(tk.v)) depth += 1;
    else if ("}])".includes(tk.v)) depth -= 1;
    if (depth < 0) return { ok: false, reason: "syntax" };
  }
  if (depth !== 0) return { ok: false, reason: "syntax" };
  const p = new Parser(src, toks);
  const node = p.value();
  if (!node || p.i !== toks.length) return { ok: false, reason: "syntax" };
  return { ok: true, node };
};

/** Whether the tree has anything the visual editor can edit beyond a single expression. */
export const isVisual = (node: MapNode | null): boolean => node === null || node.kind !== "expr";

// ---- starting points --------------------------------------------------------------------------

export type MapStart = "event" | "data" | "slim" | "empty";

export const ACCOUNT_REF = 'account.externalId != "" ? account.externalId : account.id';

export const startingMap = (start: MapStart): MapNode => {
  switch (start) {
    case "event":
      return { kind: "field", path: "event" };
    case "data":
      return { kind: "field", path: "data" };
    case "empty":
      return { kind: "object", entries: [] };
    case "slim":
      return {
        kind: "object",
        entries: [
          { key: "id", node: { kind: "field", path: "event.id" } },
          { key: "type", node: { kind: "field", path: "type" } },
          { key: "time", node: { kind: "field", path: "event.time" } },
          { key: "account", node: { kind: "expr", source: ACCOUNT_REF } },
          { key: "messageId", node: { kind: "field", path: "data.messageId" } },
          { key: "subject", node: { kind: "field", path: "data.subject" } },
          { key: "from", node: { kind: "helper", call: "from" } },
        ],
      };
  }
};

/** Default node when the user switches a value to another kind. */
export const nodeOfKind = (kind: MapKind, from?: MapNode): MapNode => {
  switch (kind) {
    case "object":
      return { kind: "object", entries: [] };
    case "list":
      return { kind: "list", items: [] };
    case "field":
      return { kind: "field", path: from?.kind === "field" ? from.path : "data.subject" };
    case "constant":
      return { kind: "constant", value: from?.kind === "constant" ? from.value : "" };
    case "secret":
      return { kind: "secret", name: from?.kind === "secret" ? from.name : "" };
    case "helper":
      return { kind: "helper", call: from?.kind === "helper" ? from.call : "from" };
    case "expr":
      return { kind: "expr", source: from ? toCel(from) : "" };
  }
};

/** A key that is not used yet in the object (`field`, `field2`, …). */
export const freshKey = (entries: MapEntry[], base = "field"): string => {
  const used = new Set(entries.map((e) => e.key));
  if (!used.has(base)) return base;
  let n = 2;
  while (used.has(`${base}${n}`)) n += 1;
  return `${base}${n}`;
};

/** Secret names a map references (env.*). */
export const secretsIn = (node: MapNode | null): string[] => {
  if (!node) return [];
  switch (node.kind) {
    case "object":
      return [...new Set(node.entries.flatMap((e) => secretsIn(e.node)))];
    case "list":
      return [...new Set(node.items.flatMap(secretsIn))];
    case "secret":
      return node.name ? [node.name] : [];
    case "expr":
      return [...node.source.matchAll(/env\.([A-Za-z_][A-Za-z0-9_]*)/g)].map((m) => m[1]!);
    default:
      return [];
  }
};

/** Duplicate keys in one object (the map would silently keep only the last). */
export const duplicateKeys = (entries: MapEntry[]): string[] => {
  const seen = new Set<string>();
  const dup = new Set<string>();
  for (const e of entries) {
    if (seen.has(e.key)) dup.add(e.key);
    seen.add(e.key);
  }
  return [...dup];
};
