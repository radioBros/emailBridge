<script setup lang="ts">
/**
 * CodeMirror 6 editor for the MJML source of a template's HTML part (G2). Lazy-loaded through
 * `components/MjmlEditor.vue`, so CodeMirror stays in its own chunk. Shares the rules editor's theme;
 * the server's compile errors are shown as diagnostics on the lines they name.
 */
import { onBeforeUnmount, onMounted, ref, shallowRef, watch } from "vue";
import { closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete";
import { defaultKeymap, history, historyKeymap, indentWithTab } from "@codemirror/commands";
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
import { celTheme } from "@/features/rules/editor/celLanguage";
import { mjmlHighlight, mjmlLanguage } from "@/features/templates/editor/mjmlLanguage";
import type { MjmlError } from "@/features/templates/helpers/mjml";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    placeholder?: string;
    ariaLabel?: string;
    errors?: MjmlError[];
    minHeight?: string;
    readonly?: boolean;
  }>(),
  {
    placeholder: "",
    ariaLabel: "",
    errors: () => [],
    minHeight: "320px",
    readonly: false,
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const host = ref<HTMLDivElement | null>(null);
const view = shallowRef<EditorView | null>(null);
const editable = new Compartment();

const applyErrors = (): void => {
  const v = view.value;
  if (!v) return;
  const doc = v.state.doc;
  const diagnostics: Diagnostic[] = props.errors
    .filter((e) => e.line != null && e.line >= 1 && e.line <= doc.lines)
    .map((e) => {
      const line = doc.line(e.line as number);
      return { from: line.from, to: Math.max(line.to, line.from), severity: "error", message: e.message };
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
      keymap.of([...closeBracketsKeymap, ...defaultKeymap, ...historyKeymap, indentWithTab]),
      placeholderExt(props.placeholder),
      celTheme,
      mjmlLanguage,
      mjmlHighlight,
      linter(null),
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

/** Moves the cursor to a 1-based line (click on an error). */
const reveal = (line: number): void => {
  const v = view.value;
  if (!v) return;
  const n = Math.min(Math.max(1, line), v.state.doc.lines);
  v.dispatch({ selection: { anchor: v.state.doc.line(n).from }, scrollIntoView: true });
  v.focus();
};

const focus = (): void => view.value?.focus();

defineExpose({ reveal, focus });
</script>

<template>
  <div
    ref="host"
    :style="{ '--mjml-min-height': minHeight }"
    class="mjml-code-editor"
    data-testid="mjml-code-editor"
  />
</template>

<style scoped lang="scss">
.mjml-code-editor {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  transition: border-color 160ms ease, box-shadow 160ms ease;

  &:focus-within {
    border-color: rgb(var(--v-theme-primary));
    box-shadow: 0 0 0 3px rgb(var(--v-theme-primary), 0.14);
  }

  :deep(.cm-editor),
  :deep(.cm-scroller) {
    min-height: var(--mjml-min-height);
  }
}
</style>
