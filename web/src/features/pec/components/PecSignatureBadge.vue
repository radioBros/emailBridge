<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { Signature } from "@/api/pec";
import { signatureBadgeOf } from "@/features/pec/helpers/signature";

const props = defineProps<{
  signature?: Signature | null;
}>();

const { t, te } = useI18n();

const badge = computed(() => signatureBadgeOf(props.signature));

const label = computed(() => {
  const key = badge.value.labelKey;
  return te(key) ? t(key) : key;
});

const tooltip = computed(() => {
  const reason = badge.value.reason;
  if (!reason) return label.value;
  if (reason.startsWith("pec.") && te(reason)) return t(reason);
  return reason;
});
</script>

<template>
  <v-chip
    v-tippy="tooltip"
    :color="badge.color"
    :prepend-icon="badge.icon"
    label
  >
    {{ label }}
  </v-chip>
</template>
