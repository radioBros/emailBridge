<script setup lang="ts" generic="T extends Record<string, unknown>">
// =========================================================================
// ServerTable — reusable server-paginated + virtually-scrolled table.
// =========================================================================

import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiMagnify } from "@mdi/js";
import { useBoxHeight, useFillHeight } from "@/composables/useFillHeight";
import { measureContentWidth, useColumnResize, type ResizeColumn } from "@/composables/useColumnResize";
import { columnMin, isFixedColumn, parsePx as headerPx } from "@/helpers/ColumnWidths";
import { useTableColumns, useTableSearch } from "@/composables/useServerTableState";
import ColManager from "./ColManager.vue";
import ColResizer from "./ColResizer.vue";
import TableFooter from "./TableFooter.vue";
import { SERVER_TABLE_DEFAULTS } from "./types";
import type { ServerTableEmits, ServerTableProps, ServerTableRow, ServerTableSlots } from "./types";

const props = withDefaults(defineProps<ServerTableProps<T>>(), SERVER_TABLE_DEFAULTS);

const emit = defineEmits<ServerTableEmits<T>>();

const slots = defineSlots<ServerTableSlots<T>>();

const { t } = useI18n();

const onRowClick = (event: Event, row: ServerTableRow<T>): void => emit("click:row", event, row);

const { searchLocal } = useTableSearch(props.search, emit);

const onIppChange = (ipp: number): void => {
  const firstRow = (props.page - 1) * props.ipp;
  emit("update:ipp", ipp);
  emit("update:page", Math.floor(firstRow / ipp) + 1);
};

const { headersN, visibleKeys, arranged, onHeadersReordered } = useTableColumns(() => props.headers);

// ---- column widths: fill the box, resize against the right neighbours -------
// Minimums come from the consumer's headers (`minWidth`), before the px widths
// below overwrite them. Fixed columns (actions, `resizable: false`) are sized to
// their content: a declared `width`, else measured from the rendered cells.

const resizeColumns = computed<ResizeColumn[]>(() =>
  arranged.value.map((h) => {
    const fixed = isFixedColumn(h);
    return { key: h.key, min: columnMin(h), fixed, width: fixed ? (headerPx(h.width) ?? undefined) : undefined };
  }),
);

const resize = useColumnResize(
  () => props.tableName,
  resizeColumns,
  () => headersN.value.map((h) => h.key),
);

const visibleHeaders = computed(() =>
  arranged.value.map((h) => {
    const w = props.resizable ? resize.width(h.key) : undefined;
    return w === undefined ? h : { ...h, width: w, minWidth: w, maxWidth: w };
  }),
);

const tableWidth = computed(() => (props.resizable && resize.sized.value ? resize.totalWidth.value : 0));

// The table's scroll box: its inner width (no scrollbar) is what the columns fill.
const bodyEl = ref<HTMLElement | null>(null);
let scrollBox: HTMLElement | null = null;
let observer: ResizeObserver | null = null;
let measureFrame = 0;

/** Rows measured for the fixed columns' content width (the virtual body renders few more). */
const MEASURE_ROWS = 50;

const updateContainer = (): void => {
  const w = scrollBox?.clientWidth ?? 0;
  // 0 while hidden (inactive tab, collapsed panel): keep the last real width.
  if (w > 0 && w !== resize.container.value) resize.container.value = w;
};

// Fixed columns get the widest one-line content of the header and the rendered
// rows. Grow-only, so paging or scrolling to shorter rows never makes the
// layout jitter.
const measureFixed = (): void => {
  const table = scrollBox?.querySelector("table");
  const cols = resizeColumns.value;
  const targets = cols.flatMap((c, i) => (c.fixed && c.width === undefined ? [i] : []));
  if (!table || !props.resizable || targets.length === 0) return;
  const rows = [table.tHead?.rows[0], ...Array.from(table.tBodies[0]?.rows ?? []).slice(0, MEASURE_ROWS)].filter(
    (r): r is HTMLTableRowElement => r !== undefined && r.cells.length === cols.length,
  );
  if (rows.length === 0) return;
  const next = { ...resize.fixedWidths.value };
  let changed = false;
  for (const i of targets) {
    const key = cols[i]!.key;
    const w = Math.max(...rows.map((r) => measureContentWidth(r.cells[i]!)));
    if (w > (next[key] ?? 0)) {
      next[key] = w;
      changed = true;
    }
  }
  if (changed) resize.fixedWidths.value = next;
};

const remeasure = (): void => {
  updateContainer();
  measureFixed();
};

// Scrolling the virtual body renders other rows: re-check them once per frame.
const onBodyScroll = (): void => {
  if (measureFrame || typeof requestAnimationFrame !== "function") return;
  measureFrame = requestAnimationFrame(() => {
    measureFrame = 0;
    measureFixed();
  });
};

onMounted(() => {
  scrollBox = bodyEl.value?.querySelector<HTMLElement>(".v-table__wrapper") ?? null;
  if (!scrollBox || !props.resizable) return;
  remeasure();
  if (typeof ResizeObserver !== "undefined") {
    observer = new ResizeObserver(updateContainer);
    observer.observe(scrollBox);
  }
  scrollBox.addEventListener("scroll", onBodyScroll, { passive: true });
});

onBeforeUnmount(() => {
  observer?.disconnect();
  scrollBox?.removeEventListener("scroll", onBodyScroll);
  if (measureFrame && typeof cancelAnimationFrame === "function") cancelAnimationFrame(measureFrame);
});

// New rows or another column set: the scrollbar may come or go and the fixed
// columns' content may be wider. `post`: measured after the DOM is patched,
// before the browser paints.
watch(
  [() => props.items, () => props.loading, () => resizeColumns.value.map((c) => c.key).join("|")],
  remeasure,
  { flush: "post" },
);

// Toolbar row: full when it holds a search or toolbar content; compact (a slim row with a small
// column button) when the column manager is alone; none when there is nothing to show.
const toolbarMode = computed<"full" | "compact" | "none">(() => {
  if (props.searchable || slots.toolbar) return "full";
  return props.showColManager ? "compact" : "none";
});

const pageCount = computed(() => Math.max(1, Math.ceil(props.total / props.ipp)));

const isItemSlot = (name: string): name is `item.${string}` => name.startsWith("item.");
const itemSlots = computed(() => Object.keys(slots).filter(isItemSlot));

const MAX_TABLE_HEIGHT = 550;
const ROW_HEIGHT = 48;
const HEADER_HEIGHT = 56;

// ---- fill height: the box ends exactly at the viewport bottom ---------------
// The root measures its own top and takes the space left (useFillHeight); the body between the
// toolbar and the footer is flex-sized, so its measured height is what the virtual table gets.
const rootEl = ref<HTMLElement | null>(null);
const isFill = computed(() => props.fillHeight && props.maxHeight == null);
const fillPx = useFillHeight(() => rootEl.value, { enabled: () => isFill.value });
const bodyPx = useBoxHeight(() => (isFill.value ? bodyEl.value : null));
const rootStyle = computed(() => ({
  ...(tableWidth.value > 0 ? { "--server-table-width": `${tableWidth.value}px` } : {}),
  ...(isFill.value && fillPx.value != null ? { height: `${fillPx.value}px` } : {}),
}));

const parsePx = (v: number | string | undefined): number | null => {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "string") {
    const n = Number.parseInt(v, 10);
    return Number.isFinite(n) ? n : null;
  }
  return null;
};

const tableHeight = computed<number>(() => {
  const capped = parsePx(props.maxHeight);
  if (capped != null) return capped;

  // Until the body is measured (first frame) the body clips a default-sized table.
  if (isFill.value) return bodyPx.value ?? MAX_TABLE_HEIGHT;

  const rows = Math.min(props.items.length || 1, 100);
  return Math.min(rows * ROW_HEIGHT + HEADER_HEIGHT, MAX_TABLE_HEIGHT);
});

// Kit bug (a): skeleton only on first load; progress bar thereafter.
// Seeded from rows, not from `loading`: a page usually starts its fetch in its own onMounted, after
// this table mounted with `loading` still false — that must still be a first load.
const hasLoadedOnce = ref(props.items.length > 0);
watch(
  () => props.items,
  () => {
    if (!props.loading) hasLoadedOnce.value = true;
  },
);
watch(
  () => props.loading,
  (loading) => {
    if (!loading && props.items.length >= 0) hasLoadedOnce.value = true;
  },
);
const isFirstLoad = computed(() => props.loading && !hasLoadedOnce.value);

// Kit bug (b): v-data-table-virtual must not sort the current page client-side.
const noClientSort = computed(() =>
  Object.fromEntries(visibleHeaders.value.map((h) => [h.key, () => 0])),
);

const rowProps = (data: { item: T }): Record<string, string> => {
  const id = String(data.item[props.itemValue] ?? "");
  return {
    "data-id": id,
    "data-testid": `row-${id}`,
  };
};
</script>

<template>
  <div
    ref="rootEl"
    :class="{
      'server-table--sized': tableWidth > 0,
      'table-fill': isFill,
    }"
    :style="rootStyle"
    class="d-flex flex-column"
  >
    <!-- Search and col manager always share one row. Desktop: search · filters · col manager;
         phone: search + col manager on top, filters below (order utilities, no media queries). -->
    <v-row
      v-if="toolbarMode !== 'none'"
      :class="toolbarMode === 'compact' ? 'py-1' : 'py-2'"
      class="ma-0 px-3 ga-3 flex-grow-0 flex-shrink-0"
      align="center"
      no-gutters
    >
      <v-col
        v-if="searchable"
        class="min-w-0 order-1 flex-2"
        sm="auto"
      >
        <v-text-field
          v-model="searchLocal"
          :placeholder="t('tables.search_placeholder')"
          :aria-label="t('tables.search_placeholder')"
          :prepend-inner-icon="mdiMagnify"
          class="no-validation min-175 max-300"
          hide-details
          clearable
        />
      </v-col>

      <v-col
        v-if="$slots.toolbar"
        cols="12"
        class="min-w-0 order-3 order-sm-2"
        sm
      >
        <div class="table-toolbar">
          <slot name="toolbar" />
        </div>
      </v-col>

      <v-col
        v-if="showColManager"
        cols="auto"
        class="ms-auto order-2 order-sm-3"
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

    <div
      ref="bodyEl"
      class="table-fill__body"
    >
      <v-data-table-virtual
        :headers="visibleHeaders"
        :items="items"
        :loading="loading"
        :loading-text="t('common.states.loading')"
        :sort-by="sortBy"
        :custom-key-sort="noClientSort"
        :height="tableHeight"
        :item-value="itemValue"
        :row-props="rowProps"
        class="bg-transparent"
        fixed-header
        @update:sort-by="emit('update:sortBy', $event)"
        @click:row="onRowClick"
      >
        <template #loader="{ isActive }">
          <v-progress-linear
            v-if="isActive && !isFirstLoad"
            :aria-label="t('common.states.refreshing')"
            color="primary"
            height="2"
            indeterminate
          />
        </template>

        <template #loading>
          <v-skeleton-loader
            v-if="isFirstLoad"
            type="table-row@6"
          />
        </template>

        <template #no-data>
          <slot name="no-data">
            <div class="table-fill__empty text-body-medium text-medium-emphasis text-center">
              {{ t("tables.no_results") }}
            </div>
          </slot>
        </template>

        <template
          v-for="h in visibleHeaders"
          :key="`header.${h.key}`"
          #[`header.${h.key}`]="headerData"
        >
          <slot
            v-if="slots[`header.${h.key}`]"
            :name="`header.${h.key}`"
            v-bind="headerData"
          />
          <div
            v-else
            class="v-data-table-header__content"
          >
            <span>{{ headerData.column.title }}</span>
            <v-icon
              v-if="headerData.column.sortable"
              :icon="headerData.getSortIcon(headerData.column)"
              class="v-data-table-header__sort-icon"
            />
          </div>
          <ColResizer
            v-if="resizable && resize.canResize(h.key)"
            :resize="resize"
            :col="h.key"
            :label="h.title"
          />
        </template>

        <template
          v-for="name in itemSlots"
          :key="name"
          #[name]="slotData"
        >
          <slot
            :name="name"
            v-bind="slotData"
          />
        </template>
      </v-data-table-virtual>
    </div>

    <TableFooter
      :page="page"
      :ipp="ipp"
      :total="total"
      :page-count="pageCount"
      class="table-fill__footer flex-shrink-0"
      @update:page="emit('update:page', $event)"
      @update:ipp="onIppChange"
    />
  </div>
</template>

<style scoped lang="scss">
// Sized: every column has its own px width (see useColumnResize) and they sum
// to the box's inner width, so the table fills it exactly. Cells stay one line
// and clip, so row heights never change under the virtual scroller.
.server-table--sized :deep(.v-table__wrapper > table) {
  table-layout: fixed;
  width: var(--server-table-width);
}

.server-table--sized :deep(.v-table__wrapper > table > thead > tr > th),
.server-table--sized :deep(.v-table__wrapper > table > tbody > tr > td) {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
</style>
