<script setup lang="ts">
import { mdiRestore } from "@mdi/js";
import { useI18n } from "vue-i18n";

defineProps<{
  colourKey: string;
  modelValue: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [string];
  reset: [];
}>();

const { t } = useI18n();
</script>

<template>
  <div class="d-flex align-center ga-2">
    <input
      :aria-label="colourKey"
      :value="modelValue"
      class="colour-swatch no-validation"
      type="color"
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    >
    <v-text-field
      :model-value="modelValue"
      :label="colourKey"
      class="no-validation flex-grow-1"
      hide-details="auto"
      @update:model-value="(v: string) => emit('update:modelValue', String(v))"
    />
    <v-btn
      :icon="mdiRestore"
      :aria-label="t('connect.branding.reset_colour')"
      @click="emit('reset')"
    />
  </div>
</template>

<style lang="scss" scoped>
.colour-swatch {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  padding: 0;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
}
</style>
