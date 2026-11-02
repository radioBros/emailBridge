<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { accountStatusOf } from "@/features/accounts/helpers/accountState";

const props = defineProps<{
  state: string;
}>();

const { t, te } = useI18n();

const spec = computed(() => accountStatusOf(props.state));

const label = computed(() => {
  const key = `accounts.state_${props.state}`;
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
