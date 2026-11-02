<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { deliveryStatusOf, isDeliveryState } from "@/features/deliveries/helpers/states";

const props = defineProps<{ state: string }>();
const { t, te } = useI18n();

const spec = computed(() => deliveryStatusOf(props.state));
const label = computed(() => {
  const key = `deliveries.state_${props.state}`;
  if (te(key)) return t(key);
  return isDeliveryState(props.state) ? props.state : props.state;
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
