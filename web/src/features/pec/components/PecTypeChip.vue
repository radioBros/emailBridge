<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { pecTypeLabelKey, pecTypeStatusOf } from "@/features/pec/helpers/receiptTypes";

const props = defineProps<{
  type: string;
}>();

const { t, te } = useI18n();

const spec = computed(() => pecTypeStatusOf(props.type));
const label = computed(() => {
  const key = pecTypeLabelKey(props.type);
  return te(key) ? t(key) : props.type;
});
</script>

<template>
  <v-chip
    :color="spec.color"
    :prepend-icon="spec.icon"
    label
  >
    {{ label }}
  </v-chip>
</template>
