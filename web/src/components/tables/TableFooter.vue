<script setup lang="ts">
// =========================================================================
// TableFooter — server-side pagination: page-size select, pager, "showing X
// to Y of Z". Layout is Vuetify utilities only (stacks below md).
// =========================================================================

import { computed } from 'vue';
import { useI18n } from 'vue-i18n';

const props = withDefaults(
  defineProps<{
    page: number;
    ipp: number;
    total: number;
    pageCount: number;
    /** At most 4 options, so a plain select is right (not AutocompleteSearch). */
    pageSizeOptions?: number[];
  }>(),
  {
    pageSizeOptions: () => [10, 25, 50, 100],
  },
);

const emit = defineEmits<{
  'update:page': [value: number];
  'update:ipp': [value: number];
}>();

const { t } = useI18n();

const pageCount = computed(() => Math.max(1, props.pageCount || 1));

const from = computed(() =>
  !props.total || !props.ipp ? 0 : Math.min((props.page - 1) * props.ipp + 1, props.total),
);

const to = computed(() => (!props.ipp ? 0 : Math.min(props.page * props.ipp, props.total)));
</script>

<template>
  <div class="d-flex flex-column flex-md-row align-start align-md-center justify-space-between flex-wrap ga-3 px-5 py-2 border-t">
    <div class="d-flex align-center ga-2 text-body-medium text-medium-emphasis text-no-wrap">
      <span>{{ t('tables.show') }}</span>

      <v-select
        :model-value="ipp"
        :items="pageSizeOptions"
        :aria-label="t('tables.items_per_page')"
        class="no-validation min-100"
        hide-details
        @update:model-value="emit('update:ipp', Number($event))"
      />

      <span>{{ t('tables.entries') }}</span>
    </div>

    <v-pagination
      :model-value="page"
      :length="pageCount"
      :total-visible="pageCount > 10 ? 5 : pageCount > 5 ? 4 : 3"
      class="flex-grow-1"
      show-first-last-page
      @update:model-value="emit('update:page', $event)"
    />

    <div class="text-body-medium text-medium-emphasis text-no-wrap">
      {{ t('tables.showing_items', { from, to, total }) }}
    </div>
  </div>
</template>
