<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import StatusChip from "@/components/common/StatusChip.vue";
import { queueStateSpec } from "@/features/queues/helpers/queues";

const props = defineProps<{ state: string }>();
const { t, te } = useI18n();

const spec = computed(() => queueStateSpec(props.state));
const label = computed(() => {
  const key = `queues.state_${props.state}`;
  return te(key) ? t(key) : props.state;
});
</script>

<template>
  <StatusChip
    :color="spec.color"
    :icon="spec.icon"
    :label="label"
  />
</template>
