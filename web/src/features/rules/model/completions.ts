import { childFields, EVENT_TYPES, eventTypeKey } from "@/features/rules/catalog/catalog";
import { HELPERS, KEYWORDS, VARIABLES, helperKey } from "@/features/rules/model/helpers";

/**
 * Autocomplete for the CEL code editor, as a pure function of the text before the cursor (the
 * CodeMirror adapter in `editor/celLanguage.ts` only maps the result). Providers:
 * payload / envelope / account fields from the event schemas, `env.` secret names, helpers with their
 * signature, event type names inside `type == "…"`, and named lists inside `inList("…`.
 */

export interface CompletionContext {
  /** Event types the rule is about (narrows `data.` fields); empty = all message types. */
  eventTypes: readonly string[];
  secretNames: readonly string[];
  listNames: readonly string[];
  /** Translates an i18n key (docs of helpers / event types). */
  describe?: (key: string) => string;
}

export type CompletionKind = "variable" | "property" | "function" | "keyword" | "constant" | "text";

export interface CompletionItem {
  label: string;
  type: CompletionKind;
  detail?: string;
  info?: string;
  /** Text to insert instead of the label; `|` marks the cursor. */
  apply?: string;
  boost?: number;
}

export interface CompletionResult {
  /** Offset (in the text before the cursor) where the replaced word starts. */
  from: number;
  options: CompletionItem[];
}

const describe = (ctx: CompletionContext, key: string): string | undefined => {
  const s = ctx.describe?.(key);
  return s && s !== key ? s : undefined;
};

const TYPE_STRING = /\btype\s*(?:==|!=)\s*"([\w.]*)$/;
const TYPE_LIST = /\btype\s+in\s+\[[^\]]*"([\w.]*)$/;
const IN_LIST = /\binList\(\s*"([\w-]*)$/;
const PATH = /([A-Za-z_][\w]*(?:\.[A-Za-z_][\w]*)*)\.([A-Za-z_]\w*)?$/;
const WORD = /[A-Za-z_]\w*$/;

/** Whether the cursor is inside a string literal (odd number of unescaped quotes). */
const inString = (text: string): boolean => {
  let quote: string | null = null;
  for (let i = 0; i < text.length; i += 1) {
    const c = text[i];
    if (quote) {
      if (c === "\\") i += 1;
      else if (c === quote) quote = null;
    } else if (c === '"' || c === "'") quote = c;
  }
  return quote !== null;
};

export const completeCel = (before: string, ctx: CompletionContext): CompletionResult | null => {
  const typeMatch = TYPE_STRING.exec(before) ?? TYPE_LIST.exec(before);
  if (typeMatch) {
    const word = typeMatch[1] ?? "";
    return {
      from: before.length - word.length,
      options: EVENT_TYPES.map((t) => ({
        label: t,
        type: "constant" as const,
        info: describe(ctx, eventTypeKey(t)),
        boost: ctx.eventTypes.includes(t) ? 2 : 0,
      })),
    };
  }
  const listMatch = IN_LIST.exec(before);
  if (listMatch) {
    const word = listMatch[1] ?? "";
    return {
      from: before.length - word.length,
      options: ctx.listNames.map((n) => ({ label: n, type: "constant" as const, detail: "list" })),
    };
  }
  if (inString(before)) return null;

  const path = PATH.exec(before);
  if (path) {
    const base = path[1]!;
    const word = path[2] ?? "";
    const from = before.length - word.length;
    if (base === "env") {
      return {
        from,
        options: ctx.secretNames.map((n) => ({ label: n, type: "variable" as const, detail: "secret" })),
      };
    }
    const root = base.split(".")[0];
    if (root === "event" || root === "data" || root === "account") {
      const options = childFields(base, ctx.eventTypes).map((f) => ({
        label: f.path.slice(base.length + 1),
        type: "property" as const,
        detail: f.type === "array" && f.items ? `list(${f.items})` : f.type,
        info: f.description,
      }));
      return options.length ? { from, options } : null;
    }
    return null;
  }

  const word = WORD.exec(before)?.[0] ?? "";
  if (!word) return null;
  const options: CompletionItem[] = [
    ...VARIABLES.map((v) => ({ label: v.name, type: "variable" as const, detail: v.detail, boost: 3 })),
    ...HELPERS.map((fn) => ({
      label: fn.name,
      type: "function" as const,
      detail: `${fn.signature} → ${fn.returns}`,
      info: describe(ctx, helperKey(fn.name)),
      apply: fn.insert,
      boost: 1,
    })),
    ...KEYWORDS.map((k) => ({ label: k, type: "keyword" as const })),
  ];
  const lower = word.toLowerCase();
  return {
    from: before.length - word.length,
    options: options.filter((o) => o.label.toLowerCase().startsWith(lower) || o.label.toLowerCase().includes(lower)),
  };
};

/** Splits an `apply` template into the text and the cursor offset. */
export const applyTemplate = (apply: string): { text: string; cursor: number } => {
  const at = apply.indexOf("|");
  if (at < 0) return { text: apply, cursor: apply.length };
  return { text: apply.slice(0, at) + apply.slice(at + 1), cursor: at };
};
