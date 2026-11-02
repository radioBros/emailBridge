<script setup lang="ts">
// =========================================================================
// ColManager — per-user column visibility + drag-to-reorder for a ServerTable
// (and the fields of a CardList), persisted through the settings store
// (server-side, per `tableName`). Also resets column widths set by dragging a
// header's edge.
//
// v-model is the list of VISIBLE keys; `update:headers` emits the full ordered
// header list (actions column always last). Forced and action columns can
// never be hidden.
//
// Needs `vuedraggable` (Vue 3 line, 4.x) and `vue-tippy`.
// =========================================================================

import { computed, onMounted, ref, watch } from 'vue';
import { mdiDragVertical, mdiTableColumn } from '@mdi/js';
import { useI18n } from 'vue-i18n';
import draggable from 'vuedraggable';
import { isActionKey, mergeColumnOrder } from '@/helpers/TableColumns';
import { useSettingsStore } from '@/stores/settings';

export interface ColManagerHeader {
  key: string;
  title: string;
  forced?: boolean;
  [key: string]: unknown;
}

const props = defineProps<{
  modelValue: string[];
  headers: ColManagerHeader[];
  /** Stable, unique key the layout is saved under. */
  tableName: string;
}>();

const emit = defineEmits<{
  'update:modelValue': [value: string[]];
  'update:headers': [value: ColManagerHeader[]];
}>();

const { t } = useI18n();
const settings = useSettingsStore();

const label = computed(() => t('tables.configure_columns'));

const isForced = (h: ColManagerHeader): boolean => h.forced === true || isActionKey(h.key);

const isShown = (h: ColManagerHeader): boolean => h.forced === true || props.modelValue.includes(h.key);

/** All non-action headers in display order (the drag list). */
const orderedHeaders = ref<ColManagerHeader[]>([]);

const emitHeaderOrder = (ordered: ColManagerHeader[]): void => {
  emit('update:headers', [...ordered, ...props.headers.filter((h) => isActionKey(h.key))]);
};

/**
 * Restores the saved layout. Runs AFTER the server settings arrive (see
 * onMounted): reading the store synchronously would make the saved layout
 * write-only, surviving a reload only via localStorage and never following the
 * user to another device.
 */
const applySavedLayout = (): void => {
  const saved = settings.getTable(props.tableName);

  // Saved order healed against the current headers: a missing key returns to
  // its DEFAULT position (not the end), a key that no longer exists is dropped.
  const headerMap = Object.fromEntries(props.headers.map((h) => [h.key, h]));
  const orderKeys = mergeColumnOrder(
    saved?.order,
    props.headers.map((h) => h.key),
  );
  orderedHeaders.value = orderKeys
    .filter((k) => !isActionKey(k) && headerMap[k])
    .map((k) => headerMap[k]!);

  // Saved visibility, or every column when nothing is saved. A key the saved
  // ORDER has never seen is a column the user never saw, so they cannot have
  // hidden it: show it.
  const forced = props.headers.filter(isForced).map((h) => h.key);
  const known = new Set(saved?.order ?? []);
  const allKeys = props.headers.map((h) => h.key);
  const base = saved?.visibility ? [...saved.visibility, ...allKeys.filter((k) => !known.has(k))] : allKeys;
  emit(
    'update:modelValue',
    [...new Set([...base, ...forced])].filter((k) => allKeys.includes(k)),
  );

  emitHeaderOrder(orderedHeaders.value);
};

onMounted(async () => {
  // load() de-duplicates in flight (N tables, one request) and never rejects:
  // a settings outage must not stop a table from rendering.
  await settings.load();
  applySavedLayout();
});

// A parent can swap its header SET after mount (columns per tab under one tableName).
watch(
  () => props.headers.map((h) => h.key).join('|'),
  () => applySavedLayout(),
);

const onDragEnd = (): void => {
  settings.setOrder(
    props.tableName,
    orderedHeaders.value.map((h) => h.key),
  );
  emitHeaderOrder(orderedHeaders.value);
};

const toggle = (key: string): void => {
  if (isActionKey(key)) return;
  const next = props.modelValue.includes(key)
    ? props.modelValue.filter((k) => k !== key)
    : [...new Set([...props.modelValue, key])];
  // Forced and action columns always stay visible.
  for (const h of props.headers) {
    if (isForced(h) && !next.includes(h.key)) next.push(h.key);
  }
  emit('update:modelValue', next);
  settings.setVisibility(
    props.tableName,
    next,
    orderedHeaders.value.map((h) => h.key),
  );
};

// ServerTable reads the widths from the store, so clearing them is enough.
const hasWidths = computed(() => Boolean(settings.getTable(props.tableName)?.widths));
const resetWidths = (): void => settings.resetWidths(props.tableName);

const draggableList = computed({
  get: () => orderedHeaders.value,
  set: (value: ColManagerHeader[]) => {
    orderedHeaders.value = value;
  },
});
</script>

<template>
  <v-menu
    :close-on-content-click="false"
    location="bottom end"
  >
    <template #activator="{ props: menuProps }">
      <!-- `#activator` swaps the button (e.g. a compact one); it must bind `props` to open the menu. -->
      <slot
        :props="menuProps"
        :label="label"
        :icon="mdiTableColumn"
        name="activator"
      >
        <v-btn-field-icon
          v-tippy="label"
          :aria-label="label"
          :icon="mdiTableColumn"
          color="primary"
          v-bind="menuProps"
        />
      </slot>
    </template>

    <v-card
      :flat="false"
      min-width="220"
      max-width="280"
      elevation="4"
    >
      <v-card-title class="text-label-small text-uppercase font-weight-bold text-medium-emphasis py-2 px-3">
        {{ label }}
      </v-card-title>

      <v-divider />

      <draggable
        v-model="draggableList"
        :animation="180"
        item-key="key"
        handle=".col-drag-handle"
        tag="div"
        @end="onDragEnd"
      >
        <!-- The checkbox carries the state; highlighting every visible row would
             light up the whole list. No comment INSIDE #item: vuedraggable needs
             exactly one child there, and dev builds keep comments as nodes
             ("Item slot must have only one child"). -->
        <template #item="{ element: header }">
          <v-list-item
            :key="header.key"
            :disabled="header.forced"
            :title="header.title"
            class="px-2"
            @click="!header.forced && toggle(header.key)"
          >
            <template #prepend>
              <!-- The drag handle only swallows the click (so it does not toggle
                   the column). A click handler on v-icon itself would make it a
                   focusable role="button" that does nothing. -->
              <span
                class="col-drag-handle d-inline-flex cursor-grab opacity-40 mr-1"
                aria-hidden="true"
                @click.stop
              >
                <v-icon-inline
                  :icon="mdiDragVertical"
                />
              </span>
              <v-checkbox-btn
                :model-value="isShown(header)"
                :disabled="header.forced"
                :aria-label="header.title"
                color="primary"
                tabindex="-1"
                @click.stop="!header.forced && toggle(header.key)"
              />
            </template>
          </v-list-item>
        </template>
      </draggable>

      <!-- Only once a column was resized: back to the automatic layout. -->
      <template v-if="hasWidths">
        <v-divider />

        <div class="d-flex justify-end pa-2">
          <v-btn-inline
            @click="resetWidths"
          >
            {{ t('tables.reset_widths') }}
          </v-btn-inline>
        </div>
      </template>
    </v-card>
  </v-menu>
</template>
