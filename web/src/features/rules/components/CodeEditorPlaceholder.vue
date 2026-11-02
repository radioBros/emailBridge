<script setup lang="ts">
import { useI18n } from "vue-i18n";

const { t } = useI18n();
</script>

<template>
  <div
    :aria-label="t('common.states.loading')"
    class="code-placeholder d-flex flex-column ga-2 pa-3"
    aria-busy="true"
    role="status"
  >
    <v-skeleton-loader
      type="text"
      class="w-75 bg-transparent"
    />
    <v-skeleton-loader
      type="text"
      class="w-50 bg-transparent"
    />
  </div>
</template>

<style scoped lang="scss">
.code-placeholder {
  min-height: 120px;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
  background: rgb(var(--v-theme-field));
}
</style>
