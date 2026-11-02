<script setup lang="ts">
/**
 * Guided filter builder. The tree (`group`) is owned by the editor and edited in place through the
 * provided tree operations; every change is reported with `change` so the editor recompiles the CEL.
 */
import type { RuleGroup } from "@/api/rules";
import BuilderGroup from "@/features/rules/components/BuilderGroup.vue";
import { createBuilderTree, provideBuilderTree } from "@/features/rules/composables/useBuilderTree";

defineProps<{
  group: RuleGroup;
  eventTypes: string[];
}>();

const emit = defineEmits<{
  change: [];
}>();

provideBuilderTree(createBuilderTree(() => emit("change")));
</script>

<template>
  <BuilderGroup
    :group="group"
    :event-types="eventTypes"
  />
</template>
