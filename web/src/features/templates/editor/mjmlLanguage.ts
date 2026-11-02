/**
 * CodeMirror 6 highlighting for MJML with Handlebars placeholders: a small stream tokenizer (tags,
 * attributes, strings, comments and `{{…}}`, also inside attribute values). Only imported by
 * `MjmlCodeEditor.vue`, which is lazy-loaded, so none of CodeMirror reaches the main bundle.
 */
import {
  HighlightStyle,
  StreamLanguage,
  syntaxHighlighting,
  type StreamParser,
  type StringStream,
} from "@codemirror/language";
import { tags } from "@lezer/highlight";

export interface MjmlState {
  inTag: boolean;
  inComment: boolean;
  quote: string | null;
}

/** Consumes one `{{…}}` / `{{{…}}}` placeholder (or the rest of the line when it is not closed). */
const eatHandlebars = (stream: StringStream): void => {
  const triple = Boolean(stream.match("{{{"));
  if (!triple) stream.match("{{");
  const close = triple ? "}}}" : "}}";
  if (stream.skipTo(close)) stream.match(close);
  else stream.skipToEnd();
};

export const mjmlParser: StreamParser<MjmlState> = {
  name: "mjml",
  startState: () => ({ inTag: false, inComment: false, quote: null }),
  copyState: (s) => ({ ...s }),
  token: (stream, state) => {
    if (state.inComment) {
      if (stream.skipTo("-->")) {
        stream.match("-->");
        state.inComment = false;
      } else {
        stream.skipToEnd();
      }
      return "comment";
    }
    if (stream.match("{{", false)) {
      eatHandlebars(stream);
      return "variableName.special";
    }
    if (state.quote) {
      const q = state.quote;
      while (!stream.eol()) {
        if (stream.match("{{", false)) break;
        if (stream.next() === q) {
          state.quote = null;
          break;
        }
      }
      return "string";
    }
    if (!state.inTag) {
      if (stream.match("<!--")) {
        state.inComment = true;
        return "comment";
      }
      if (stream.match(/^<\/?[A-Za-z][\w:-]*/)) {
        state.inTag = true;
        return "tagName";
      }
      while (!stream.eol()) {
        if (stream.match("{{", false) || stream.match("<", false)) break;
        stream.next();
      }
      return null;
    }
    if (stream.eatSpace()) return null;
    if (stream.match("/>") || stream.match(">")) {
      state.inTag = false;
      return "angleBracket";
    }
    if (stream.match(/^[A-Za-z_:][\w:.-]*/)) return "attributeName";
    if (stream.eat("=")) return "operator";
    const c = stream.next();
    if (c === '"' || c === "'") {
      state.quote = c;
      return "string";
    }
    return null;
  },
};

export const mjmlLanguage = StreamLanguage.define(mjmlParser);

const v = (name: string, alpha = 1): string => `rgba(var(--v-theme-${name}), ${alpha})`;

const highlight = HighlightStyle.define([
  { tag: tags.tagName, color: v("primary"), fontWeight: "600" },
  { tag: tags.attributeName, color: v("secondary") },
  { tag: tags.string, color: v("success") },
  { tag: tags.special(tags.variableName), color: v("accent"), fontWeight: "600" },
  { tag: tags.comment, color: v("on-surface", 0.5), fontStyle: "italic" },
  { tag: tags.angleBracket, color: v("on-surface", 0.6) },
  { tag: tags.operator, color: v("on-surface", 0.6) },
]);

export const mjmlHighlight = syntaxHighlighting(highlight);
