<script setup lang="ts">
import { computed } from "vue";
import { buildHtmlSrcdoc } from "@/features/sandbox/helpers/mimeParse";

const props = defineProps<{
  html: string;
  loadImages: boolean;
}>();

const srcdoc = computed(() => buildHtmlSrcdoc(props.html, props.loadImages));
</script>

<template>
  <iframe
    :srcdoc="srcdoc"
    class="sandbox-html-frame w-100 rounded-lg"
    sandbox=""
    referrerpolicy="no-referrer"
    title="HTML"
  />
</template>

<style scoped lang="scss">
.sandbox-html-frame {
  min-height: 280px;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface));
}
</style>
