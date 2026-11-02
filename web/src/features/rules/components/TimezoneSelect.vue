<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";

defineProps<{
  modelValue: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const { t } = useI18n();

const zones = computed((): string[] => {
  try {
    const all = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone");
    if (all?.length) return ["UTC", ...all.filter((z) => z !== "UTC")];
  } catch {
    // older engines: fall through
  }
  return ["UTC", "Europe/Rome", "Europe/London", "Europe/Berlin", "America/New_York"];
});
</script>

<template>
  <AutocompleteSearch
    :model-value="modelValue"
    :items="zones"
    :label="t('rules.builder.timezone')"
    class="no-validation"
    hide-details
    @update:model-value="(v) => emit('update:modelValue', String(v ?? 'UTC'))"
  />
</template>
