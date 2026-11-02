<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { tokenizeCel, CEL_MAX_BYTES } from "@/features/routes/celHighlight";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    label: string;
    hint?: string;
    rows?: number;
    rules?: Array<(v: unknown) => true | string>;
  }>(),
  { rows: 6, hint: undefined, rules: () => [] },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const local = ref(props.modelValue);

watch(
  () => props.modelValue,
  (v) => {
    if (v !== local.value) local.value = v;
  },
);

const tokens = computed(() => tokenizeCel(local.value || " "));

const sizeRule = (v: unknown): true | string => {
  const s = typeof v === "string" ? v : "";
  return new TextEncoder().encode(s).length <= CEL_MAX_BYTES || `≤ ${CEL_MAX_BYTES} bytes`;
};

const allRules = computed(() => [...props.rules, sizeRule]);

const onInput = (v: string | number | null): void => {
  const s = v == null ? "" : String(v);
  local.value = s;
  emit("update:modelValue", s);
};
</script>

<template>
  <div class="cel-editor">
    <div class="text-caption text-medium-emphasis mb-1">
      {{ label }}
    </div>
    <div
      :style="{ minHeight: `${rows * 1.4 + 1.5}rem` }"
      class="cel-editor__frame"
    >
      <pre
        aria-hidden="true"
        class="cel-editor__highlight font-mono"
      ><span
        v-for="(tok, i) in tokens"
        :key="i"
        :class="`cel-tok cel-tok--${tok.kind}`"
      >{{ tok.text }}</span></pre>
      <textarea
        :value="local"
        :rows="rows"
        :aria-label="label"
        class="cel-editor__input font-mono"
        spellcheck="false"
        autocomplete="off"
        @input="onInput(($event.target as HTMLTextAreaElement).value)"
      />
    </div>
    <div
      v-if="hint"
      class="text-caption text-medium-emphasis mt-1"
    >
      {{ hint }}
    </div>
    <!-- keep Vuetify rules via a hidden field for form.validate() -->
    <v-text-field
      :model-value="local"
      :rules="allRules"
      class="d-none"
    />
  </div>
</template>

<style scoped lang="scss">
.cel-editor__frame {
  position: relative;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 8px;
  background: rgb(var(--v-theme-surface-variant), 0.25);
  overflow: hidden;
}

.cel-editor__highlight,
.cel-editor__input {
  margin: 0;
  padding: 12px;
  box-sizing: border-box;
  width: 100%;
  min-height: inherit;
  font-size: 0.8125rem;
  line-height: 1.4;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  tab-size: 2;
}

.cel-editor__highlight {
  position: absolute;
  inset: 0;
  pointer-events: none;
  overflow: hidden;
  color: rgb(var(--v-theme-on-surface), 0.85);
}

.cel-editor__input {
  position: relative;
  z-index: 1;
  border: 0;
  outline: none;
  resize: vertical;
  background: transparent;
  color: transparent;
  caret-color: rgb(var(--v-theme-primary));
  font-family: inherit;
}

.cel-tok--keyword {
  color: rgb(var(--v-theme-accent));
  font-weight: 600;
}

.cel-tok--function {
  color: rgb(var(--v-theme-info));
}

.cel-tok--string {
  color: rgb(var(--v-theme-success));
}

.cel-tok--number {
  color: rgb(var(--v-theme-warning));
}

.cel-tok--operator {
  color: rgb(var(--v-theme-secondary));
}

.cel-tok--comment {
  color: rgb(var(--v-theme-on-surface), 0.45);
  font-style: italic;
}

.cel-tok--punct {
  color: rgb(var(--v-theme-on-surface), 0.6);
}

.cel-tok--ident,
.cel-tok--plain {
  color: rgb(var(--v-theme-on-surface), 0.9);
}
</style>
