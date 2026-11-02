<script setup lang="ts">
import { computed } from "vue";
import type { SearchHighlight } from "@/features/search/helpers/api";
import { snippetSegments } from "@/features/search/helpers/snippet";

const props = defineProps<{
  text: string;
  highlights?: SearchHighlight[];
}>();

/** Plain text only: the matches become <mark>, nothing is ever rendered as HTML. */
const segments = computed(() => snippetSegments(props.text, props.highlights ?? []));
</script>

<template>
  <span class="text-body-small text-medium-emphasis">
    <template
      v-for="(seg, i) in segments"
      :key="i"
    >
      <mark
        v-if="seg.match"
        class="search-hit text-high-emphasis font-weight-medium"
      >{{ seg.text }}</mark>
      <template v-else>
        {{ seg.text }}
      </template>
    </template>
  </span>
</template>

<style scoped lang="scss">
.search-hit {
  background: rgb(var(--v-theme-warning), 0.22);
  border-radius: 2px;
  color: inherit;
  padding: 0 1px;
}
</style>
