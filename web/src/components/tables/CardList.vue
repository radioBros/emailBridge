<script setup lang="ts" generic="T extends Record<string, unknown>">
// =========================================================================
// CardList — the card view of a ServerTable: same props, emits and slots, so
// a consumer swaps one for the other without touching its bindings
// (TableCard does the swap by breakpoint).
//
//   • Server-driven like the table: the PARENT owns page/ipp/sortBy/search and
//     fetches. Pages ACCUMULATE here into one infinite list
//     (helpers/CardList#mergePage): page 1 starts over, page N replaces its
//     own slots. `loadMode`: 'scroll' loads the next page on reaching the end,
//     'button' shows a "Load more" button.
//   • Virtualised by ROW: `grid` gives cards per row by breakpoint
//     (`{ xs: 1, md: 2, xl: 3 }`), and each row of N cards is ONE virtual
//     entry, measured as a whole.
//   • Scrolls inside its own box (`maxHeight`, the fill height, or a
//     viewport-relative cap): Vuetify's virtual list measures offsets against
//     its scroller, which is only reliable for a positioned scroll container.
//     `screenBox` (phones, set by TableCard): the box is as tall as the screen
//     below the app bar, so the page scrolls until the header is gone and the
//     list then fills the screen and scrolls inside.
//   • `#card` replaces the card (gets item, index, columns, clickable, select);
//     the default CardListItem renders each value through the consumer's
//     `#item.<key>` slots. `#card-skeleton` is the first-load placeholder for a
//     custom card.
//   • Search, sort and column visibility/order behave exactly as in the table
//     (shared composables; ColManager picks the fields a card shows). `ipp` is
//     the page size of each load; `resizable` only applies to rows.
//   • After a mutation, set page to 1 to refresh: a re-fetch of a later page
//     cannot know an earlier one changed.
//
// Requires Vuetify 4 (v-row `density`, the gap-based grid).
// =========================================================================

import { computed, getCurrentInstance, onMounted, ref, shallowRef, watch } from 'vue';
import { mdiMagnify, mdiSortAscending, mdiSortDescending } from '@mdi/js';
import { useI18n } from 'vue-i18n';
import { useDisplay, useLocale } from 'vuetify';
import AutocompleteSearch from '@/components/AutocompleteSearch.vue';
import AppSectionLoader from '@/components/states/AppSectionLoader.vue';
import { useBoxHeight, useFillHeight, useScreenBoxHeight } from '@/composables/useFillHeight';
import { useTableColumns, useTableSearch } from '@/composables/useServerTableState';
import { chunkRows, mergePage, resolveGridColumns } from '@/helpers/CardList';
import { isActionKey } from '@/helpers/TableColumns';
import CardListItem from './CardListItem.vue';
import ColManager from './ColManager.vue';
import vuetifyDefaults from '@/core/config/VuetifyDefaults';
import { CARD_LIST_DEFAULTS } from './types';
import type { CardListProps, CardListSlots, CardSlotProps, ServerTableEmits, ServerTableSortItem } from './types';

type LoadStatus = 'ok' | 'empty' | 'error' | 'loading';

const props = withDefaults(defineProps<CardListProps<T>>(), CARD_LIST_DEFAULTS);

const emit = defineEmits<ServerTableEmits<T>>();

const slots = defineSlots<CardListSlots<T>>();

const { t } = useI18n();
// Vuetify's own strings ($vuetify.*), resolved the way its components resolve them.
const { t: vt } = useLocale();

/** Row-action sizes for the phone cards — same as desktop rows (VuetifyDefaults.CardList). */
const cardDefaults = vuetifyDefaults.CardList;
const { name: breakpoint } = useDisplay();
const instance = getCurrentInstance();

/** Default height cap when fillHeight is off: most of the viewport, own scroller. */
const DEFAULT_MAX_HEIGHT = 'min(75dvh, 760px)';
/** Start loading the next page this far before the end, so scrolling rarely meets the spinner. */
const PREFETCH_MARGIN = 400;

const { searchLocal } = useTableSearch(props.search, emit);
const { headersN, visibleKeys, arranged, onHeadersReordered } = useTableColumns(() => props.headers);

// ---- fill height: the box ends exactly at the viewport bottom ---------------
// The root measures its own top and takes the space left (useFillHeight); the list scrolls inside
// the flex-sized body under the toolbar, so the body's measured height is the list's cap.
const rootEl = ref<HTMLElement | null>(null);
const bodyEl = ref<HTMLElement | null>(null);
const isFill = computed(() => props.fillHeight && props.maxHeight == null && !props.screenBox);
const screenBoxHeight = useScreenBoxHeight();
const fillPx = useFillHeight(() => rootEl.value, { enabled: () => isFill.value });
const bodyPx = useBoxHeight(() => (isFill.value ? bodyEl.value : null));

const listMaxHeight = computed(() => {
  if (props.maxHeight != null) return props.maxHeight;
  // Screen box: as tall as the screen below the app bar.
  if (props.screenBox) return screenBoxHeight.value;
  // Until the body is measured (first frame) the body clips the default cap.
  if (isFill.value) return bodyPx.value ?? DEFAULT_MAX_HEIGHT;
  return DEFAULT_MAX_HEIGHT;
});

const rootFillClass = computed(() => (isFill.value ? 'table-fill' : undefined));
const rootStyle = computed(() => (isFill.value && fillPx.value != null ? { height: `${fillPx.value}px` } : undefined));

// ---- click: only a consumer listening to click:row makes cards interactive --

// Read per render (vnode props are not reactive): a listener added later re-renders this component.
const hasRowClick = (): boolean => typeof instance?.vnode.props?.['onClick:row'] === 'function';

const select = (item: T, event: Event): void => emit('click:row', event, { item });

// ---- sort: the column picker a header row would otherwise be ----------------

const sortOptions = computed(() =>
  headersN.value
    .filter((h) => h.sortable !== false && !isActionKey(h.key))
    .map((h) => ({ title: h.title, value: h.key })),
);
const sortKey = computed(() => props.sortBy[0]?.key ?? null);
const sortOrder = computed(() => props.sortBy[0]?.order ?? 'asc');
const sortOrderLabel = computed(() =>
  sortOrder.value === 'asc' ? t('tables.sort_ascending') : t('tables.sort_descending'),
);

// Toolbar row: full with a search, a sort or toolbar content; compact (a slim row with a small
// column button) when the column manager is alone; none when there is nothing to show.
const toolbarMode = computed<'full' | 'compact' | 'none'>(() => {
  if (props.searchable || sortOptions.value.length || slots.toolbar) return 'full';
  return props.showColManager ? 'compact' : 'none';
});

const emitSort = (next: ServerTableSortItem[]): void => {
  emit('update:page', 1);
  emit('update:sortBy', next);
};

const onSortKey = (key: unknown): void =>
  emitSort(typeof key === 'string' && key ? [{ key, order: sortOrder.value }] : []);

const toggleSortOrder = (): void => {
  if (sortKey.value) emitSort([{ key: sortKey.value, order: sortOrder.value === 'asc' ? 'desc' : 'asc' }]);
};

// ---- grid: cards per row by breakpoint, grouped into virtual rows -----------

const columns = computed(() => resolveGridColumns(props.grid, breakpoint.value));
/** Card grid: N equal columns with the toolbar's 12 px gap (ga-3), nothing else between cards. */
const gridStyle = computed(() => ({ display: 'grid', gridTemplateColumns: `repeat(${columns.value}, minmax(0, 1fr))` }));
const skeletonCount = computed(() => columns.value * 3);

const keyOf = (item: T): unknown => item[props.itemValue];

// ---- pages accumulate into one list ------------------------------------------

const rows = shallowRef<T[]>([...props.items]);
const cardRows = computed(() => chunkRows(rows.value, columns.value, keyOf));
const hasMore = computed(() => rows.value.length < props.total);
const countLabel = computed(() =>
  t('tables.showing_items', { from: 1, to: rows.value.length, total: props.total }),
);

/** Bumped when page 1 arrives: remounts the infinite scroll, resetting its "no more" state. */
const generation = ref(0);

// Derived from the one `loading` prop. "Loaded once" flips on SUCCESS only (items arriving), so a
// retry after a failed first load is still a first load: a skeleton, never an overlay over nothing.
// Seeded from rows, not from `loading`: the page's fetch usually starts after this list mounted.
const hasLoadedOnce = ref(props.items.length > 0);
const loadingMore = ref(false);
const isFirstLoad = computed(() => props.loading && !hasLoadedOnce.value);
const isRefreshing = computed(() => props.loading && hasLoadedOnce.value && !loadingMore.value);

/**
 * The infinite scroll waits on `done`. `requested`: this component asked for the next page (so a load
 * that ends without items failed); otherwise it only waited for a load already in flight.
 */
let pending: { done: (status: LoadStatus) => void; requested: boolean } | null = null;

const settle = (status: LoadStatus): void => {
  const waiting = pending;
  pending = null;
  loadingMore.value = false;
  waiting?.done(status);
};

watch(
  () => props.items,
  (items) => {
    if (props.page <= 1) {
      rows.value = [...items];
      generation.value += 1;
      pending = null; // belonged to the infinite scroll that is being replaced
    } else {
      rows.value = mergePage(rows.value, items, props.page, props.ipp, keyOf);
    }
    hasLoadedOnce.value = true;
    settle(hasMore.value ? 'ok' : 'empty');
  },
);

// 'post': runs after the items watcher of the same flush, so arriving items are never mistaken for a
// load that ended empty-handed.
watch(
  () => props.loading,
  (loading) => {
    if (loading || !pending) return;
    settle(pending.requested ? 'error' : hasMore.value ? 'ok' : 'empty');
  },
  { flush: 'post' },
);

const onLoad = ({ done }: { done: (status: LoadStatus) => void }): void => {
  // A load is already in flight (the first one, or a refetch): wait for it instead of piling on.
  if (props.loading || !hasLoadedOnce.value) {
    pending = { done, requested: false };
    return;
  }
  if (!hasMore.value) {
    done('empty');
    return;
  }
  pending = { done, requested: true };
  loadingMore.value = true;
  emit('update:page', props.page + 1);
};

// The list always starts at the top: entering card view on page N (e.g. switching from the table)
// reloads from page 1 rather than showing page N as if it were the first.
onMounted(() => {
  if (props.page <= 1) return;
  rows.value = [];
  hasLoadedOnce.value = false;
  emit('update:page', 1);
});

// ---- slots -------------------------------------------------------------------

const isItemSlot = (name: string): name is `item.${string}` => name.startsWith('item.');
const itemSlots = computed(() => Object.keys(slots).filter(isItemSlot));

const cardProps = (item: T, index: number): CardSlotProps<T> => ({
  item,
  index,
  columns: arranged.value,
  clickable: hasRowClick(),
  select: (event: Event) => select(item, event),
});
</script>

<template>
  <div
    ref="rootEl"
    :class="rootFillClass"
    :style="rootStyle"
    class="d-flex flex-column"
  >
    <!-- Toolbar: search and col manager always share the first row; sort and filters follow
         (full width on phone, inline on sm+), via order utilities. -->
    <v-row
      v-if="toolbarMode !== 'none'"
      :class="toolbarMode === 'compact' ? 'py-1' : 'py-2'"
      class="ma-0 px-3 ga-3 flex-grow-0 flex-shrink-0"
      align="center"
      no-gutters
    >
      <v-col
        v-if="searchable"
        class="flex-grow-1 min-w-0 order-1"
        sm="auto"
      >
        <v-text-field
          v-model="searchLocal"
          :placeholder="t('tables.search_placeholder')"
          :aria-label="t('tables.search_placeholder')"
          :prepend-inner-icon="mdiMagnify"
          class="no-validation"
          hide-details
          clearable
        />
      </v-col>

      <v-col
        v-if="sortOptions.length"
        :cols="searchable ? 12 : undefined"
        :class="searchable ? 'order-3 order-sm-2' : 'order-1'"
        sm="auto"
        class="flex-grow-1"
      >
        <div class="d-flex align-center ga-1">
          <AutocompleteSearch
            :model-value="sortKey"
            :items="sortOptions"
            :label="t('tables.sort_by')"
            class="no-validation flex-grow-1"
            clearable
            hide-details
            @update:model-value="onSortKey"
          />

          <v-btn-field-icon
            v-tippy="sortOrderLabel"
            :aria-label="sortOrderLabel"
            :icon="sortOrder === 'asc' ? mdiSortAscending : mdiSortDescending"
            :disabled="!sortKey"
            @click="toggleSortOrder"
          />
        </div>
      </v-col>

      <v-col
        v-if="$slots.toolbar"
        cols="12"
        class="min-w-0 order-4 order-sm-3"
        sm
      >
        <div class="table-toolbar">
          <slot name="toolbar" />
        </div>
      </v-col>

      <v-col
        v-if="showColManager"
        cols="auto"
        class="ms-auto order-2 order-sm-4"
      >
        <ColManager
          v-model="visibleKeys"
          :headers="headersN"
          :table-name="tableName"
          @update:headers="onHeadersReordered"
        >
          <!-- Alone in the toolbar: a compact button, so the row stays slim. -->
          <template
            v-if="toolbarMode === 'compact'"
            #activator="{ props: activatorProps, label, icon }"
          >
            <v-btn-affix
              v-tippy="label"
              :aria-label="label"
              :icon="icon"
              color="primary"
              v-bind="activatorProps"
            />
          </template>
        </ColManager>
      </v-col>
    </v-row>

    <!-- First load: real cards with the skeleton inside, laid out on the same grid. -->
    <div
      v-if="isFirstLoad"
      :style="gridStyle"
      class="px-3 pb-3 ga-3"
    >
      <div
        v-for="n in skeletonCount"
        :key="n"
      >
        <slot name="card-skeleton">
          <v-card>
            <v-skeleton-loader type="heading, list-item-two-line, list-item-two-line" />
          </v-card>
        </slot>
      </div>
    </div>

    <div
      v-else
      ref="bodyEl"
      class="position-relative table-fill__body"
    >
      <slot
        v-if="!rows.length"
        name="no-data"
      >
        <div class="table-fill__empty text-body-medium text-medium-emphasis text-center">
          {{ t('tables.no_results') }}
        </div>
      </slot>

      <!-- Keyed by generation: page 1 (a new search or sort) starts a fresh list, scrolled to the top. -->
      <v-infinite-scroll
        v-else
        :key="generation"
        :mode="loadMode === 'button' ? 'manual' : 'intersect'"
        :max-height="listMaxHeight"
        :margin="PREFETCH_MARGIN"
        color="primary"
        class="position-relative px-3 pt-1"
        @load="onLoad"
      >
        <!-- Renderless: the infinite scroll is the scroller. One entry per ROW of N cards. -->
        <v-defaults-provider :defaults="cardDefaults">
          <v-virtual-scroll
            :items="cardRows"
            item-key="key"
            renderless
          >
            <template #default="{ item: row, index: rowIndex, itemRef }">
              <div
                :ref="itemRef"
                :style="gridStyle"
                class="pb-3 ga-3"
              >
                <div
                  v-for="(item, i) in row.items"
                  :key="String(keyOf(item))"
                  :data-id="keyOf(item)"
                  :data-testid="`card-${keyOf(item)}`"
                  class="min-w-0"
                >
                  <slot
                    name="card"
                    v-bind="cardProps(item, rowIndex * columns + i)"
                  >
                    <CardListItem
                      :item="item"
                      :index="rowIndex * columns + i"
                      :columns="arranged"
                      :clickable="hasRowClick()"
                      @select="select(item, $event)"
                    >
                      <template
                        v-for="name in itemSlots"
                        :key="name"
                        #[name]="cell"
                      >
                        <slot
                          :name="name"
                          v-bind="cell"
                        />
                      </template>
                    </CardListItem>
                  </slot>
                </div>
              </div>
            </template>
          </v-virtual-scroll>
        </v-defaults-provider>

        <!-- In intersect mode Vuetify renders this slot for the idle "ok" state as well as while
             loading, so it stays on screen until a load reports "empty". Show the spinner only while
             more items can still arrive; once everything is loaded, show the count instead. -->
        <template #loading>
          <div
            v-if="hasMore || loadingMore"
            class="d-flex justify-center py-3"
          >
            <v-progress-circular
              :aria-label="t('common.states.loading')"
              color="primary"
              width="2"
              indeterminate
            />
          </div>
          <span
            v-else
            class="text-body-small text-medium-emphasis"
          >
            {{ countLabel }}
          </span>
        </template>

        <template #load-more="{ props: loadMore }">
          <v-btn
            v-if="hasMore"
            color="primary"
            v-bind="loadMore"
          >
            {{ vt('$vuetify.infiniteScroll.loadMore') }}
          </v-btn>
          <span
            v-else
            class="text-body-small text-medium-emphasis"
          >
            {{ countLabel }}
          </span>
        </template>

        <template #empty>
          <span class="text-body-small text-medium-emphasis">
            {{ countLabel }}
          </span>
        </template>

        <template #error="{ props: retry }">
          <div class="d-flex align-center ga-3">
            <span class="text-body-small text-medium-emphasis">
              {{ t('tables.load_more_failed') }}
            </span>
            <v-btn-inline
              color="primary"
              v-bind="retry"
            >
              {{ t('tables.retry') }}
            </v-btn-inline>
          </div>
        </template>
      </v-infinite-scroll>

      <!-- Refetch (search, sort): the cards stay readable under the overlay. Loading MORE uses the
           infinite scroll's own spinner at the end instead. -->
      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>
