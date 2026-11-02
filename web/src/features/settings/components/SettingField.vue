<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiLock } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { SettingValue } from "@/api/settings";
import { SOURCE_COLORS, type SettingFieldDef } from "@/features/settings/catalog";
import { rulesForField } from "@/features/settings/helpers/rules";
import { useSettingLabels } from "@/features/settings/composables/useSettingLabels";

const props = defineProps<{
  field: SettingFieldDef;
  entry?: SettingValue;
  modelValue: unknown;
  locked: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: unknown];
}>();

const { t, te } = useI18n();

const { fieldLabel, fieldHint } = useSettingLabels();

const label = computed(() => fieldLabel(props.field.key));

const hint = computed(() => fieldHint(props.field.key));

const source = computed(() => props.entry?.source ?? "default");

const sourceColor = computed(() => SOURCE_COLORS[source.value] ?? "grey");

const sourceLabel = computed(() => {
  const key = `settings.source.${source.value}`;
  return te(key) ? t(key) : source.value;
});

const sourceHelp = computed(() => {
  const key = `settings.source_help.${source.value}`;
  return te(key) ? t(key) : sourceLabel.value;
});

const rules = computed(() =>
  rulesForField(props.field.kind, props.field.key, props.locked),
);

const selectItems = computed(() =>
  (props.field.options ?? []).map((v) => ({
    title: te(`settings.option.${v}`) ? t(`settings.option.${v}`) : v,
    value: v,
  })),
);

/** More than 5 options → AutocompleteSearch (owner rule); ≤5 stay a plain v-select. */
const useAutocomplete = computed(() => selectItems.value.length > 5);

const selectModel = computed(() =>
  props.modelValue == null ? null : String(props.modelValue),
);

const stringsText = computed({
  get: () => {
    const v = props.modelValue;
    if (Array.isArray(v)) return v.join(", ");
    return v == null ? "" : String(v);
  },
  set: (raw: string) => {
    const parts = raw
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    emit("update:modelValue", parts);
  },
});

const onBool = (v: boolean | null): void => {
  emit("update:modelValue", Boolean(v));
};

const onNumber = (v: string | number | null): void => {
  if (v === null || v === "") {
    emit("update:modelValue", null);
    return;
  }
  const n = typeof v === "number" ? v : Number(v);
  emit("update:modelValue", Number.isFinite(n) ? n : v);
};

const onText = (v: unknown): void => {
  emit("update:modelValue", v == null ? "" : String(v));
};
</script>

<template>
  <div class="setting-field d-flex flex-column ga-1 mb-4">
    <div class="d-flex align-center flex-wrap ga-2 mb-1">
      <span class="text-body-medium font-weight-medium">
        <slot
          :label="label"
          name="label"
        >
          {{ label }}
        </slot>
      </span>
      <v-chip
        v-tippy="sourceHelp"
        :color="sourceColor === 'grey' ? undefined : sourceColor"
        label
      >
        {{ sourceLabel }}
      </v-chip>
      <v-icon-inline
        v-if="locked"
        :icon="mdiLock"
        :aria-label="t('settings.locked')"
        class="text-medium-emphasis"
      />
      <slot name="meta" />
    </div>

    <slot name="subtitle" />

    <v-switch
      v-if="field.kind === 'bool'"
      :model-value="Boolean(modelValue)"
      :aria-label="label"
      :disabled="locked"
      :hint="hint"
      :persistent-hint="Boolean(hint)"
      :rules="rules"
      color="success"
      hide-details="auto"
      @update:model-value="onBool"
    />

    <v-text-field
      v-else-if="field.kind === 'int' || field.kind === 'float'"
      :model-value="modelValue == null ? '' : String(modelValue)"
      :aria-label="label"
      :disabled="locked"
      :hint="hint"
      :persistent-hint="Boolean(hint)"
      :suffix="field.unit"
      :rules="rules"
      type="number"
      hide-details="auto"
      @update:model-value="onNumber"
    />

    <AutocompleteSearch
      v-else-if="field.kind === 'select' && useAutocomplete"
      :model-value="selectModel"
      :items="selectItems"
      :aria-label="label"
      :disabled="locked"
      :hint="hint"
      :persistent-hint="Boolean(hint)"
      :rules="rules"
      hide-details="auto"
      @update:model-value="onText"
    />

    <v-select
      v-else-if="field.kind === 'select'"
      :model-value="modelValue"
      :items="selectItems"
      :aria-label="label"
      :disabled="locked"
      :hint="hint"
      :persistent-hint="Boolean(hint)"
      :rules="rules"
      hide-details="auto"
      @update:model-value="onText"
    />

    <v-text-field
      v-else-if="field.kind === 'strings'"
      v-model="stringsText"
      :aria-label="label"
      :disabled="locked"
      :hint="hint || t('settings.strings_hint')"
      :rules="rules"
      hide-details="auto"
      persistent-hint
    />

    <v-text-field
      v-else
      :model-value="modelValue == null ? '' : String(modelValue)"
      :aria-label="label"
      :disabled="locked"
      :hint="hint"
      :persistent-hint="Boolean(hint)"
      :rules="rules"
      :type="field.key.toLowerCase().includes('pass') ? 'password' : 'text'"
      hide-details="auto"
      @update:model-value="onText"
    />
  </div>
</template>
