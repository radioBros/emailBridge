<script setup lang="ts">
import { computed } from "vue";
import { highlightSegments, type MatchRange } from "@/features/settings/helpers/search";

const props = defineProps<{
  text: string;
  ranges?: MatchRange[];
}>();

/** Plain text only: matched parts become <mark>, nothing is ever rendered as HTML. */
const segments = computed(() => highlightSegments(props.text, props.ranges ?? []));
</script>

<template>
  <span>
    <template
      v-for="(seg, i) in segments"
      :key="i"
    >
      <mark
        v-if="seg.match"
        class="settings-hit"
      >{{ seg.text }}</mark>
      <!-- Inline on purpose: surrounding whitespace would split words around a <mark>. -->
      <template v-else>{{ seg.text }}</template>
    </template>
  </span>
</template>

<style scoped lang="scss">
.settings-hit {
  background: rgb(var(--v-theme-warning), 0.24);
  border-radius: 2px;
  color: inherit;
  font-weight: inherit;
  padding: 0 1px;
}
</style>
