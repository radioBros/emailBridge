<script setup lang="ts">
/**
 * Lazy front of the MJML code editor: the CodeMirror chunk loads the first time an MJML part is shown.
 * The placeholder is the rules editor's static, editor-shaped block (a loader that must itself be fetched
 * could not cover the wait).
 */
import { defineAsyncComponent, ref, type Component } from "vue";
import CodeEditorPlaceholder from "@/features/rules/components/CodeEditorPlaceholder.vue";

defineOptions({ inheritAttrs: false });

// Loose type: the wrapper forwards every prop through $attrs.
const Inner: Component = defineAsyncComponent({
  loader: () => import("@/features/templates/editor/MjmlCodeEditor.vue"),
  loadingComponent: CodeEditorPlaceholder,
  delay: 0,
});

interface EditorApi {
  reveal: (line: number) => void;
  focus: () => void;
}

const inner = ref<EditorApi | null>(null);

defineExpose({
  reveal: (line: number): void => inner.value?.reveal(line),
  focus: (): void => inner.value?.focus(),
});
</script>

<template>
  <Inner
    ref="inner"
    v-bind="$attrs"
  />
</template>
