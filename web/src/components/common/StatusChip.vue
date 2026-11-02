<script setup lang="ts">
import type { StatusColor } from "@/constants/Statuses";

withDefaults(
  defineProps<{
    color?: StatusColor | string;
    icon?: string;
    label?: string;
  }>(),
  {
    color: "primary",
    icon: undefined,
    label: undefined,
  },
);
</script>

<template>
  <v-chip
    :color="color === 'grey' ? undefined : color"
    :prepend-icon="icon"
    label
  >
    <slot>{{ label }}</slot>
  </v-chip>
</template>
