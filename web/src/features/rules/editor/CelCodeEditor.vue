<script setup lang="ts">
/**
 * CodeMirror 6 editor for CEL (and JSON for the test panel's paste mode). Lazy-loaded through
 * `components/CodeEditor.vue`, so CodeMirror is its own chunk.
 */
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import { useI18n } from "vue-i18n";
import { closeBrackets, closeBracketsKeymap, completionKeymap, startCompletion } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
import { json, jsonParseLinter } from "@codemirror/lang-json";
import { bracketMatching, indentOnInput } from "@codemirror/language";
import { linter, setDiagnostics, type Diagnostic } from "@codemirror/lint";
import { Compartment, EditorState } from "@codemirror/state";
import {
  EditorView,
  highlightActiveLine,
  highlightActiveLineGutter,
  keymap,
  lineNumbers,
  placeholder as placeholderExt,
} from "@codemirror/view";
import { celAutocomplete, celHighlight, celLanguage, celTheme } from "@/features/rules/editor/celLanguage";
import type { CompletionContext } from "@/features/rules/model/completions";
import { offsetOf, type CodeError } from "@/features/rules/model/code";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    language?: "cel" | "json";
    placeholder?: string;
    ariaLabel?: string;
    errors?: CodeError[];
    eventTypes?: string[];
    secretNames?: string[];
    listNames?: string[];
    minHeight?: string;
    readonly?: boolean;
  }>(),
  {
    language: "cel",
    placeholder: "",
    ariaLabel: "",
    errors: () => [],
    eventTypes: () => [],
    secretNames: () => [],
    listNames: () => [],
    minHeight: "120px",
    readonly: false,
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const { t } = useI18n();
const host = ref<HTMLDivElement | null>(null);
const view = shallowRef<EditorView | null>(null);
const editable = new Compartment();

const completionCtx = (): CompletionContext => ({
  eventTypes: props.eventTypes,
  secretNames: props.secretNames,
  listNames: props.listNames,
  describe: (key: string) => t(key),
});

const languageExtensions = () =>
  props.language === "json"
    ? [json(), linter(jsonParseLinter(), { delay: 300 })]
    : [celLanguage, celHighlight, celAutocomplete(completionCtx), linter(null)];

const applyErrors = (): void => {
  const v = view.value;
  if (!v || props.language === "json") return;
  const text = v.state.doc.toString();
  const diagnostics: Diagnostic[] = props.errors.map((e) => {
    const from = offsetOf(text, e.line, e.column);
    const to = Math.min(text.length, Math.max(from + 1, text.indexOf("\n", from) === -1 ? text.length : text.indexOf("\n", from)));
    return { from, to: Math.max(to, from), severity: "error", message: e.message };
  });
  v.dispatch(setDiagnostics(v.state, diagnostics));
};

onMounted(() => {
  if (!host.value) return;
  const state = EditorState.create({
    doc: props.modelValue,
    extensions: [
      lineNumbers(),
      highlightActiveLineGutter(),
      highlightActiveLine(),
      history(),
      indentOnInput(),
      bracketMatching(),
      closeBrackets(),
      EditorView.lineWrapping,
      keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, ...completionKeymap, indentWithTab]),
      placeholderExt(props.placeholder),
      celTheme,
      ...languageExtensions(),
      editable.of([EditorView.editable.of(!props.readonly), EditorState.readOnly.of(props.readonly)]),
      EditorView.contentAttributes.of({ "aria-label": props.ariaLabel, "aria-multiline": "true" }),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) emit("update:modelValue", u.state.doc.toString());
      }),
    ],
  });
  view.value = new EditorView({ state, parent: host.value });
  applyErrors();
});

onBeforeUnmount(() => {
  view.value?.destroy();
  view.value = null;
});

watch(
  () => props.modelValue,
  (next) => {
    const v = view.value;
    if (!v) return;
    const cur = v.state.doc.toString();
    if (cur === next) return;
    v.dispatch({ changes: { from: 0, to: cur.length, insert: next } });
  },
);

watch(() => props.errors, applyErrors, { deep: true });

watch(
  () => props.readonly,
  (ro) => {
    view.value?.dispatch({
      effects: editable.reconfigure([EditorView.editable.of(!ro), EditorState.readOnly.of(ro)]),
    });
  },
);

/** Inserts text at the cursor (snippets). */
const insert = (text: string): void => {
  const v = view.value;
  if (!v) return;
  const { from, to } = v.state.selection.main;
  v.dispatch({ changes: { from, to, insert: text }, selection: { anchor: from + text.length } });
  v.focus();
};

const focus = (): void => view.value?.focus();

/** Opens the completion list at the cursor (Ctrl+Space does the same). */
const complete = (): void => {
  const v = view.value;
  if (!v) return;
  v.focus();
  startCompletion(v);
};

/** Moves the cursor to a 1-based line / column (click on an error). */
const reveal = (line: number, column: number): void => {
  const v = view.value;
  if (!v) return;
  const pos = offsetOf(v.state.doc.toString(), line, column);
  v.dispatch({ selection: { anchor: pos }, scrollIntoView: true });
  v.focus();
};

defineExpose({ insert, focus, complete, reveal });
</script>

<template>
  <div
    ref="host"
    :style="{ '--cel-min-height': minHeight }"
    class="cel-code-editor"
    data-testid="cel-code-editor"
  />
</template>

<style scoped lang="scss">
.cel-code-editor {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  transition: border-color 160ms ease, box-shadow 160ms ease;

  &:focus-within {
    border-color: rgb(var(--v-theme-primary));
    box-shadow: 0 0 0 3px rgb(var(--v-theme-primary), 0.14);
  }

  :deep(.cm-editor) {
    min-height: var(--cel-min-height);
  }

  :deep(.cm-scroller) {
    min-height: var(--cel-min-height);
  }
}
</style>
