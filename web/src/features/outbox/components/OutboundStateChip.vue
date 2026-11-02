<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { outboundStatusOf } from "@/features/outbox/helpers/outboundState";

const props = defineProps<{ state: string }>();
const { t, te } = useI18n();

const spec = computed(() => outboundStatusOf(props.state));
const label = computed(() => {
  const key = `outbox.state_${props.state}`;
  return te(key) ? t(key) : props.state;
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
