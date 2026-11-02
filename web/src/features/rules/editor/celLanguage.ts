/**
 * CodeMirror 6 pieces for CEL: a stream tokenizer (highlighting), a theme that reads the Vuetify theme
 * tokens (so light / dark follow the app toggle without reconfiguring), and the completion source that
 * adapts `model/completions.ts`. Only imported by `CelCodeEditor.vue`, which is lazy-loaded, so none of
 * CodeMirror reaches the main bundle.
 */
import { autocompletion, type Completion, type CompletionSource } from "@codemirror/autocomplete";
import { HighlightStyle, StreamLanguage, syntaxHighlighting, type StreamParser } from "@codemirror/language";
import { EditorView } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { HELPERS, KEYWORDS, VARIABLES } from "@/features/rules/model/helpers";
import { applyTemplate, completeCel, type CompletionContext } from "@/features/rules/model/completions";

const FUNCTIONS = new Set([...HELPERS.map((h) => h.name), "size", "matches", "startsWith", "endsWith", "contains", "exists", "all", "map", "filter", "exists_one", "string", "int", "double", "timestamp", "duration"]);
const VARS = new Set(VARIABLES.map((v) => v.name));
const WORDS = new Set(KEYWORDS);

interface CelState {
  afterDot: boolean;
}

const celParser: StreamParser<CelState> = {
  name: "cel",
  startState: () => ({ afterDot: false }),
  token: (stream, state) => {
    if (stream.eatSpace()) return null;
    if (stream.match("//")) {
      stream.skipToEnd();
      return "comment";
    }
    const ch = stream.peek();
    if (ch === '"' || ch === "'") {
      const q = stream.next()!;
      let escaped = false;
      while (!stream.eol()) {
        const c = stream.next();
        if (escaped) escaped = false;
        else if (c === "\\") escaped = true;
        else if (c === q) break;
      }
      state.afterDot = false;
      return "string";
    }
    if (stream.match(/^-?\d+(\.\d+)?([eE][+-]?\d+)?[uU]?/)) {
      state.afterDot = false;
      return "number";
    }
    if (stream.match(/^[A-Za-z_]\w*/)) {
      const word = stream.current();
      const wasDot = state.afterDot;
      state.afterDot = false;
      if (wasDot) return stream.peek() === "(" ? "function" : "propertyName";
      if (WORDS.has(word)) return word === "true" || word === "false" || word === "null" ? "atom" : "keyword";
      if (stream.peek() === "(" && FUNCTIONS.has(word)) return "function";
      if (VARS.has(word)) return "variableName";
      return "variableName.special";
    }
    if (stream.match(/^(&&|\|\||==|!=|<=|>=|[<>!?:+\-*/%])/)) {
      state.afterDot = false;
      return "operator";
    }
    const c = stream.next();
    state.afterDot = c === ".";
    return c === "." ? "punctuation" : "bracket";
  },
  tokenTable: {
    "variableName.special": tags.special(tags.variableName),
    function: tags.function(tags.variableName),
    propertyName: tags.propertyName,
  },
};

export const celLanguage = StreamLanguage.define(celParser);

const v = (name: string, alpha = 1): string => `rgba(var(--v-theme-${name}), ${alpha})`;

const highlight = HighlightStyle.define([
  { tag: tags.keyword, color: v("accent"), fontWeight: "600" },
  { tag: tags.atom, color: v("accent") },
  { tag: tags.string, color: v("success") },
  { tag: tags.number, color: v("warning") },
  { tag: tags.function(tags.variableName), color: v("info") },
  { tag: tags.variableName, color: v("primary"), fontWeight: "600" },
  { tag: tags.special(tags.variableName), color: v("on-surface") },
  { tag: tags.propertyName, color: v("secondary") },
  { tag: tags.operator, color: v("on-surface", 0.7) },
  { tag: tags.comment, color: v("on-surface", 0.5), fontStyle: "italic" },
  { tag: tags.punctuation, color: v("on-surface", 0.6) },
  { tag: tags.bracket, color: v("on-surface", 0.6) },
]);

/** Editor chrome on the Vuetify tokens; one theme serves light and dark. */
export const celTheme = EditorView.theme({
  "&": {
    color: v("on-surface"),
    backgroundColor: v("field"),
    fontSize: "13.5px",
    borderRadius: "12px",
  },
  "&.cm-focused": { outline: "none" },
  ".cm-scroller": {
    fontFamily: '"JetBrains Mono", "Cascadia Code", ui-monospace, SFMono-Regular, Menlo, monospace',
    lineHeight: "1.6",
  },
  ".cm-content": { padding: "10px 0", caretColor: v("primary") },
  ".cm-line": { padding: "0 12px" },
  ".cm-gutters": {
    backgroundColor: "transparent",
    color: v("on-surface", 0.45),
    border: "none",
    borderTopLeftRadius: "12px",
    borderBottomLeftRadius: "12px",
  },
  ".cm-activeLine": { backgroundColor: v("primary", 0.05) },
  ".cm-activeLineGutter": { backgroundColor: "transparent", color: v("on-surface", 0.8) },
  ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": {
    backgroundColor: `${v("primary", 0.22)} !important`,
  },
  ".cm-cursor": { borderLeftColor: v("primary"), borderLeftWidth: "2px" },
  ".cm-placeholder": { color: v("on-surface", 0.45) },
  ".cm-tooltip": {
    backgroundColor: v("surface"),
    color: v("on-surface"),
    border: `1px solid ${v("border-color", 0.6)}`,
    borderRadius: "10px",
    boxShadow: "0 10px 30px -12px rgba(15, 23, 42, 0.35)",
    overflow: "hidden",
  },
  ".cm-tooltip-autocomplete > ul": {
    fontFamily: '"JetBrains Mono", ui-monospace, monospace',
    maxHeight: "18em",
  },
  ".cm-tooltip-autocomplete > ul > li": { padding: "4px 10px", lineHeight: "1.5" },
  ".cm-tooltip-autocomplete > ul > li[aria-selected]": {
    backgroundColor: v("primary", 0.14),
    color: v("on-surface"),
  },
  ".cm-completionDetail": { color: v("on-surface", 0.55), fontStyle: "normal", marginLeft: "10px" },
  ".cm-completionInfo": {
    padding: "8px 10px",
    maxWidth: "320px",
    fontFamily: "inherit",
    lineHeight: "1.45",
  },
  ".cm-completionIcon": { opacity: "0.7" },
  ".cm-diagnostic-error": { borderLeftColor: v("error") },
  ".cm-lintRange-error": {
    backgroundImage: "none",
    textDecoration: `wavy underline ${v("error")}`,
    textUnderlineOffset: "3px",
  },
  ".cm-lint-marker-error": { content: "none" },
  ".cm-panels": { backgroundColor: v("surface"), color: v("on-surface") },
});

export const celHighlight = syntaxHighlighting(highlight);

const ICON: Record<string, string> = {
  variable: "variable",
  property: "property",
  function: "function",
  keyword: "keyword",
  constant: "constant",
  text: "text",
};

/** CodeMirror completion source over the pure provider. `ctx()` is read on every request. */
export const celCompletionSource = (ctx: () => CompletionContext): CompletionSource => (context) => {
  const before = context.state.sliceDoc(Math.max(0, context.pos - 400), context.pos);
  const res = completeCel(before, ctx());
  if (!res) return null;
  if (!res.options.length) return null;
  const offset = context.pos - before.length;
  const options: Completion[] = res.options.map((o) => ({
    label: o.label,
    type: ICON[o.type],
    detail: o.detail,
    info: o.info,
    boost: o.boost,
    apply: o.apply
      ? (view, _completion, from, to) => {
          const { text, cursor } = applyTemplate(o.apply!);
          view.dispatch({
            changes: { from, to, insert: text },
            selection: { anchor: from + cursor },
          });
        }
      : undefined,
  }));
  return { from: offset + res.from, options, validFor: /^[\w.]*$/ };
};

export const celAutocomplete = (ctx: () => CompletionContext) =>
  autocompletion({
    override: [celCompletionSource(ctx)],
    activateOnTyping: true,
    icons: true,
    maxRenderedOptions: 80,
  });
