<script setup lang="ts">
/**
 * Lazy front of the CodeMirror editor: the CodeMirror chunk loads the first time a code editor is
 * shown. The placeholder is a static, editor-shaped block (a loader that must itself be fetched could
 * not cover the wait).
 */
import { defineAsyncComponent, ref, type Component } from "vue";
import CodeEditorPlaceholder from "@/features/rules/components/CodeEditorPlaceholder.vue";

defineOptions({ inheritAttrs: false });

// Loose type: the wrapper forwards every prop through $attrs.
const Inner: Component = defineAsyncComponent({
  loader: () => import("@/features/rules/editor/CelCodeEditor.vue"),
  loadingComponent: CodeEditorPlaceholder,
  delay: 0,
});

interface EditorApi {
  insert: (text: string) => void;
  focus: () => void;
  complete: () => void;
  reveal: (line: number, column: number) => void;
}

const inner = ref<EditorApi | null>(null);

defineExpose({
  insert: (text: string): void => inner.value?.insert(text),
  focus: (): void => inner.value?.focus(),
  complete: (): void => inner.value?.complete(),
  reveal: (line: number, column: number): void => inner.value?.reveal(line, column),
});
</script>

<template>
  <Inner
    ref="inner"
    v-bind="$attrs"
  />
</template>
