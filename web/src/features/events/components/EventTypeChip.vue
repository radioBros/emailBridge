<script setup lang="ts">
import { computed } from "vue";
import { familyColor } from "@/features/events/helpers/types";

const props = defineProps<{ type: string }>();

const color = computed(() => familyColor(props.type));
</script>

<template>
  <v-chip
    :color="color"
    label
  >
    {{ type }}
  </v-chip>
</template>
