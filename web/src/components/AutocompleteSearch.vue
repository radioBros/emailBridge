<template>
  <v-select
    ref="selectEl"
    v-model="value"
    :items="filtered"
    :item-title="titleKey"
    :item-value="valueKey"
    :rules="rules"
    :loading="loading"
    :menu-props="{ ...menuProps, class: 'select-search-menu' }"
    :class="{ 'has-value': hasValue }"
    class="select-search"
    v-bind="$attrs"
    @update:menu="onMenuChange"
  >
    <template #prepend-item>
      <div
        class="position-sticky top-0 bg-background px-2 pb-0 pt-2"
        style="z-index: 2;"
      >
        <!--
          variant: kit menu-search chrome — unique (not a page field). Its compact
          density comes from the nested VSelect.VTextField default in VuetifyDefaults.
        -->
        <v-text-field
          ref="searchInputEl"
          v-model="searchText"
          :placeholder="t('common.search')"
          :aria-label="t('common.search')"
          :loading="serverSearch && loading"
          :prepend-inner-icon="mdiMagnify"
          variant="outlined"
          autocomplete="off"
          type="search"
          data-lpignore="true"
          data-testid="autocomplete-search-input"
          class="no-validation"
          hide-details
          v-bind="searchAtts ?? {}"
          @keydown.space.stop
          @keydown.tab.stop
        />
        <v-divider class="mt-2 mb-0" />
      </div>
    </template>

    <template #item="data">
      <slot
        :props="data.props"
        :item="data.item"
        :index="data.index"
        name="item"
      >
        <v-list-item
          :title="getItemTitle(data.item)"
          :data-testid="getItemValue(data.item)"
          v-bind="data.props"
        />
      </slot>
    </template>

    <template
      v-if="slots.selection"
      #selection="data"
    >
      <slot
        :item="data.item"
        :index="data.index"
        name="selection"
      />
    </template>

    <template
      v-if="slots.chip"
      #chip="data"
    >
      <slot
        :props="data.props"
        :item="data.item"
        :index="data.index"
        name="chip"
      />
    </template>

    <template
      v-if="slots.append"
      #append-item
    >
      <slot name="append" />
    </template>

    <template
      v-if="slots['append-outer']"
      #append
    >
      <slot name="append-outer" />
    </template>
  </v-select>
</template>

<script setup lang="ts">
// =========================================================================
// AutocompleteSearch — searchable select: the one to use for ANY select with
// more than 5 options. Kit structure: v-select + sticky search in #prepend-item.
//
//   • Client mode (default): filters `items` locally on the title plus
//     `searchFields`. For small, static lists (countries, enums).
//   • Server mode (`server-search`): emits a debounced `update:search`; the
//     parent fetches and passes the matching `items` (+ `loading`). For
//     org-wide pickers (users, customers): never ship the whole collection to
//     filter it client-side.
//
// Every other attribute (label, placeholder, clearable, disabled, density,
// multiple ...) goes straight to the v-select. Slots: #item, #selection,
// #chip, #append (end of the list), #append-outer (after the field).
// Needs `common.search` from lang/*/common.json.
// =========================================================================

import { computed, nextTick, onBeforeUnmount, ref, useSlots, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiMagnify } from "@mdi/js";

type ItemValue = string | number;
type ModelValue =
  | ItemValue
  | ItemValue[]
  | Record<string, unknown>
  | Array<Record<string, unknown>>
  | null;

interface Item {
  title?: string;
  value?: ItemValue;
  [key: string]: unknown;
}

defineOptions({ inheritAttrs: false });

const props = withDefaults(
  defineProps<{
    modelValue: ModelValue;
    items: Array<Item | string>;
    rules?: Array<(value: unknown) => true | string>;
    itemTitle?: string;
    itemValue?: string;
    /** Extra item fields the local filter matches (client mode). */
    searchFields?: string[];
    /** Server mode: emit the search text instead of filtering locally. */
    serverSearch?: boolean;
    loading?: boolean;
    /** Extra attributes for the search input. */
    searchAtts?: Record<string, unknown> | null;
    menuProps?: Record<string, unknown>;
  }>(),
  {
    rules: () => [],
    itemTitle: "title",
    itemValue: "value",
    searchFields: () => [],
    serverSearch: false,
    loading: false,
    searchAtts: null,
    menuProps: () => ({}),
  },
);

const emit = defineEmits<{
  "update:modelValue": [value: ModelValue];
  "update:search": [value: string];
}>();

const { t } = useI18n();
const slots = useSlots();

const selectEl = ref<{ reset: () => void } | null>(null);
const searchInputEl = ref<{ focus: () => void } | null>(null);
const searchText = ref("");

/**
 * `""` is "no value" unless an item really carries `""` (an "All" option). Filter refs often
 * start as `""`; Vuetify counts that as dirty and shows the clear (x) on an empty field.
 */
const hasEmptyItem = computed(() =>
  props.items.some((item) => (typeof item === "string" ? item : getByPath(item, props.itemValue)) === ""),
);
const isUnset = (v: ModelValue): boolean => v == null || (v === "" && !hasEmptyItem.value);

const value = computed({
  get: () => (isUnset(props.modelValue) ? null : props.modelValue),
  set: (next: ModelValue) => emit("update:modelValue", next),
});

const hasValue = computed(() =>
  Array.isArray(props.modelValue) ? props.modelValue.length > 0 : !isUnset(props.modelValue),
);

const titleKey = computed(() => props.itemTitle);
const valueKey = computed(() => props.itemValue);

// ---- menu open: clear the search and focus it --------------------------------

const onMenuChange = async (open: boolean): Promise<void> => {
  if (!open) return;
  searchText.value = "";
  await nextTick();
  searchInputEl.value?.focus();
};

// ---- item helpers (support dotted item-title / item-value paths) -------------

// In Vuetify 4 the #item slot's `data.item` is the ORIGINAL object from `:items`.
const getByPath = (obj: unknown, path: string): unknown =>
  path.split(".").reduce<unknown>((acc, key) => (acc as Record<string, unknown> | undefined)?.[key], obj);

const read = (item: unknown, key: string): string => {
  const v = getByPath(item, key);
  return v == null ? "" : String(v);
};

const getItemTitle = (item: unknown): string => read(item, titleKey.value);
const getItemValue = (item: unknown): string => read(item, valueKey.value);

// ---- filtering -----------------------------------------------------------------

/** Plain string arrays become `{ title, value }` items. */
const normalized = computed<Item[]>(() =>
  props.items.map((item) => (typeof item === "string" ? { title: item, value: item } : item)),
);

const matches = (item: Item, text: string): boolean =>
  [getItemTitle(item), ...props.searchFields.map((field) => read(item, field))]
    .join(" ")
    .toLowerCase()
    .includes(text.toLowerCase());

const filtered = computed<Item[]>(() => {
  const text = searchText.value.trim();
  if (props.serverSearch || !text) return normalized.value;
  return normalized.value.filter((item) => matches(item, text));
});

// ---- server mode: debounced search emit ------------------------------------------

let debounce: ReturnType<typeof setTimeout> | undefined;

watch(searchText, (text) => {
  if (!props.serverSearch) return;
  clearTimeout(debounce);
  debounce = setTimeout(() => emit("update:search", (text ?? "").trim()), 300);
});

onBeforeUnmount(() => clearTimeout(debounce));

const reset = (): void => {
  searchText.value = "";
  selectEl.value?.reset();
};

defineExpose({ reset });
</script>
