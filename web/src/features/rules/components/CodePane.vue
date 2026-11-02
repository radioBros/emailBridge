<script setup lang="ts">
/** Code mode: the CEL editor with snippets, format, autocomplete on demand and fullscreen. */
import { computed, onBeforeUnmount, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiArrowCollapse,
  mdiArrowExpand,
  mdiAutoFix,
  mdiCodeTags,
  mdiFormatAlignLeft,
  mdiLightbulbOnOutline,
} from "@mdi/js";
import CodeEditor from "@/features/rules/components/CodeEditor.vue";
import { SNIPPETS, formatCel, type CodeError, type Snippet } from "@/features/rules/model/code";

const props = withDefaults(
  defineProps<{
    modelValue: string;
    target: "filter" | "map";
    errors?: CodeError[];
    eventTypes?: string[];
    secretNames?: string[];
    listNames?: string[];
  }>(),
  { errors: () => [], eventTypes: () => [], secretNames: () => [], listNames: () => [] },
);

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const { t } = useI18n();
const editor = ref<InstanceType<typeof CodeEditor> | null>(null);
const fullscreen = ref(false);

const snippets = computed(() => SNIPPETS.filter((s) => s.target === props.target));

const useSnippet = (s: Snippet): void => {
  if (props.target === "map" || !props.modelValue.trim()) {
    emit("update:modelValue", s.code);
    return;
  }
  editor.value?.insert(` && ${s.code}`);
};

const format = (): void => emit("update:modelValue", formatCel(props.modelValue, props.target));

const onKey = (e: KeyboardEvent): void => {
  if (e.key === "Escape" && fullscreen.value) fullscreen.value = false;
};

watch(fullscreen, (on) => {
  if (on) window.addEventListener("keydown", onKey);
  else window.removeEventListener("keydown", onKey);
});

onBeforeUnmount(() => window.removeEventListener("keydown", onKey));

defineExpose({
  reveal: (line: number, column: number): void => editor.value?.reveal(line, column),
});
</script>

<template>
  <div
    :class="{ 'code-pane--full pa-4 pa-md-6': fullscreen }"
    :data-testid="`code-pane-${target}`"
    class="d-flex flex-column ga-2"
  >
    <div class="d-flex flex-wrap align-center ga-1">
      <v-menu location="bottom start">
        <template #activator="{ props: menuProps }">
          <v-btn-inline
            :prepend-icon="mdiCodeTags"
            color="secondary"
            data-testid="code-snippets"
            v-bind="menuProps"
          >
            {{ t("rules.code.snippets") }}
          </v-btn-inline>
        </template>
        <v-list
          :aria-label="t('rules.code.snippets')"
          max-width="420"
        >
          <v-list-item
            v-for="s in snippets"
            :key="s.id"
            :title="t(`rules.snippet.${s.id}`)"
            :subtitle="s.code.replace(/\s+/g, ' ')"
            :prepend-icon="mdiLightbulbOnOutline"
            @click="useSnippet(s)"
          />
        </v-list>
      </v-menu>
      <v-btn-inline
        :prepend-icon="mdiAutoFix"
        color="primary"
        data-testid="code-complete"
        @click="editor?.complete()"
      >
        {{ t("rules.code.complete") }}
      </v-btn-inline>
      <v-btn-inline
        :prepend-icon="mdiFormatAlignLeft"
        color="info"
        @click="format"
      >
        {{ t("rules.code.format") }}
      </v-btn-inline>
      <v-spacer />
      <span class="text-caption text-medium-emphasis d-none d-md-inline">{{ t("rules.code.shortcut") }}</span>
      <v-btn-inline
        :icon="fullscreen ? mdiArrowCollapse : mdiArrowExpand"
        :aria-label="fullscreen ? t('rules.code.exit_fullscreen') : t('rules.code.fullscreen')"
        :aria-pressed="fullscreen"
        data-testid="code-fullscreen"
        @click="fullscreen = !fullscreen"
      />
    </div>
    <CodeEditor
      ref="editor"
      :model-value="modelValue"
      :aria-label="target === 'filter' ? t('rules.code.filter_label') : t('rules.code.map_label')"
      :placeholder="target === 'filter' ? t('rules.code.filter_placeholder') : t('rules.code.map_placeholder')"
      :errors="errors"
      :event-types="eventTypes"
      :secret-names="secretNames"
      :list-names="listNames"
      :min-height="fullscreen ? 'calc(100dvh - 140px)' : target === 'filter' ? '96px' : '200px'"
      @update:model-value="(v: string) => emit('update:modelValue', v)"
    />
  </div>
</template>

<style scoped lang="scss">
.code-pane--full {
  position: fixed;
  inset: 0;
  z-index: 2400;
  background: rgb(var(--v-theme-background));
}
</style>
