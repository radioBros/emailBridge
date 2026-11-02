<script setup lang="ts">
/** Read-only JSON with colour (keys, strings, numbers, literals) and a copy button. No v-html. */
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCheck, mdiContentCopy } from "@mdi/js";

const props = withDefaults(
  defineProps<{
    value: unknown;
    maxHeight?: string;
  }>(),
  { maxHeight: "360px" },
);

const { t } = useI18n();
const copied = ref(false);

const text = computed((): string => {
  try {
    return JSON.stringify(props.value, null, 2) ?? "null";
  } catch {
    return String(props.value);
  }
});

interface Tok {
  s: string;
  c: string;
}

const TOKEN = /("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g;

const tokens = computed((): Tok[] => {
  const out: Tok[] = [];
  let last = 0;
  for (const m of text.value.matchAll(TOKEN)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ s: text.value.slice(last, at), c: "" });
    if (m[1]) {
      out.push({ s: m[1], c: m[2] ? "json-key" : "json-string" });
      if (m[2]) out.push({ s: m[2], c: "" });
    } else if (m[3]) out.push({ s: m[3], c: "json-literal" });
    else out.push({ s: m[4] ?? "", c: "json-number" });
    last = at + m[0].length;
  }
  if (last < text.value.length) out.push({ s: text.value.slice(last), c: "" });
  return out;
});

const copy = async (): Promise<void> => {
  try {
    await navigator.clipboard.writeText(text.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1400);
  } catch {
    copied.value = false;
  }
};
</script>

<template>
  <div class="json-view position-relative">
    <v-btn-affix
      :icon="copied ? mdiCheck : mdiContentCopy"
      :color="copied ? 'success' : undefined"
      :aria-label="t('rules.copy')"
      class="json-copy"
      @click="copy"
    />
    <pre
      :style="{ maxHeight }"
      class="json-pre pa-3 ma-0"
      tabindex="0"
    ><span
      v-for="(tok, i) in tokens"
      :key="i"
      :class="tok.c"
    >{{ tok.s }}</span></pre>
  </div>
</template>

<style scoped lang="scss">
.json-view {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  background: rgb(var(--v-theme-field));
}

.json-copy {
  position: absolute;
  top: 6px;
  right: 6px;
  z-index: 1;
}

.json-pre {
  overflow: auto;
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
  line-height: 1.6;
  white-space: pre;
}

.json-key {
  color: rgb(var(--v-theme-secondary));
}

.json-string {
  color: rgb(var(--v-theme-success));
}

.json-number {
  color: rgb(var(--v-theme-warning));
}

.json-literal {
  color: rgb(var(--v-theme-accent));
}
</style>
