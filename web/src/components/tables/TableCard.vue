<script setup lang="ts" generic="T extends Record<string, unknown>">
// =========================================================================
// TableCard — a ServerTable that turns into cards on small screens: rows from
// the mobile breakpoint up, a CardList below it, or cards everywhere.
//
// Drop-in for ServerTable: the same props, v-models, events and slots
// (./types), plus the card options:
//   • `cards`: force cards everywhere, `true` or with their own grid
//     (`:cards="{ xs: 1, md: 2, xl: 3 }"`)
//   • `mobileBreakpoint`: below it cards show (default: the app's Vuetify
//     setting)
//   • `grid`: cards per row by breakpoint, mobile first (`{ xs: 1, md: 2, xl: 3 }`)
//   • `loadMode`: 'scroll' (next page on reaching the end) or 'button' ("Load more")
//   • below the mobile breakpoint the cards scroll in a box as tall as the
//     screen below the app bar (`screenBox`), not a fill height
//   • `#card` / `#card-skeleton` slots.
// It owns the card too: both views render inside an AppCard, so a page never
// wraps a table in its own card. `#card-header` fills the card's header bar
// (title, actions); without it there is no header. The content has no padding:
// the toolbar and footer carry their own, and the fill-height box is the card
// body, so the footer sits on the card's bottom edge. That box measures where it sits and ends
// exactly at the viewport bottom (composables/useFillHeight).
// Column slots (`#item.<key>`) feed both views, so a column looks the same as
// a cell and as a card field.
// =========================================================================

import { computed, getCurrentInstance } from 'vue';
import { useDisplay } from 'vuetify';
import AppCard from '@/components/cards/AppCard.vue';
import CardList from './CardList.vue';
import ServerTable from './ServerTable.vue';
import { TABLE_CARD_DEFAULTS } from './types';
import type {
  CardListSlots,
  CardSlotProps,
  TableCardSlots,
  ServerTableCellProps,
  ServerTableEmits,
  ServerTableRow,
  ServerTableSortItem,
  TableCardProps,
} from './types';

const props = withDefaults(defineProps<TableCardProps<T>>(), TABLE_CARD_DEFAULTS);

const emit = defineEmits<ServerTableEmits<T>>();

const slots = defineSlots<TableCardSlots<T>>();

const instance = getCurrentInstance();

// `mobile: null` (not undefined): without a breakpoint of our own, Vuetify falls back to the app's.
const display = useDisplay({
  mobile: null,
  get mobileBreakpoint() {
    return props.mobileBreakpoint;
  },
});

const showCards = computed(() => Boolean(props.cards) || display.mobile.value);

// ---- props: each view gets the shared contract plus its own options --------

// A grid given to `cards` wins over `grid`: it is the layout of the forced card view.
// Below the mobile breakpoint the cards scroll in a screen-tall box (the page scrolls the header away).
const cardListProps = computed(() => {
  const { cards, mobileBreakpoint: _breakpoint, ...rest } = props;
  const base = { ...rest, screenBox: rest.screenBox || display.mobile.value };
  return typeof cards === 'object' ? { ...base, grid: cards } : base;
});

// The card follows its view's height: the fill box, or toolbar + screen box on phones.
const autoHeight = computed(
  () => (props.fillHeight && props.maxHeight == null) || (showCards.value && display.mobile.value),
);

const serverTableProps = computed(() => {
  const {
    cards: _cards,
    mobileBreakpoint: _breakpoint,
    loadMode: _loadMode,
    grid: _grid,
    screenBox: _screenBox,
    ...rest
  } = props;
  return rest;
});

// ---- events: forwarded as-is -------------------------------------------------

// click:row only when the consumer listens: a listener is what makes rows/cards look clickable.
// Read per render, because vnode props are not reactive.
const listeners = () => ({
  'update:page': (value: number) => emit('update:page', value),
  'update:ipp': (value: number) => emit('update:ipp', value),
  'update:sortBy': (value: ServerTableSortItem[]) => emit('update:sortBy', value),
  'update:search': (value: string) => emit('update:search', value),
  ...(typeof instance?.vnode.props?.['onClick:row'] === 'function'
    ? { 'click:row': (event: Event, row: ServerTableRow<T>) => emit('click:row', event, row) }
    : {}),
});

// ---- slots: everything to the cards; the card-only ones never reach the table,
// and `card-header` belongs to the AppCard, not to either view.

type ForwardedSlot = keyof CardListSlots<T> & string;

const isCardOnly = (name: string): boolean => name === 'card' || name === 'card-skeleton';

const slotNames = computed(() => Object.keys(slots).filter((name) => name !== 'card-header') as ForwardedSlot[]);
const tableSlotNames = computed(() => slotNames.value.filter((name) => !isCardOnly(name)));

// A forwarded slot hands its props on untouched; each slot's own type is checked where the consumer
// writes it, against CardListSlots / ServerTableSlots.
type AnySlotProps = { column: { key: string | null; title?: string } } & ServerTableCellProps<T> & CardSlotProps<T>;
const passThrough = (data: unknown) => (data ?? {}) as AnySlotProps;
</script>

<template>
  <!-- A fill-height view sizes itself to the viewport, a phone card list to its screen box: the
       card follows it (height auto, inline so it beats AppCard's default 100 % — the utility class
       is not !important in Vuetify 4), instead of 100 % of a stretched column, which leaves blank
       space under the footer or overflows. -->
  <AppCard
    :style="autoHeight ? { height: 'auto' } : undefined"
    content-class="pa-0"
  >
    <template
      v-if="slots['card-header']"
      #header
    >
      <slot name="card-header" />
    </template>

    <CardList
      v-if="showCards"
      v-bind="cardListProps"
      v-on="listeners()"
    >
      <template
        v-for="name in slotNames"
        :key="name"
        #[name]="data"
      >
        <slot
          :name="name"
          v-bind="passThrough(data)"
        />
      </template>
    </CardList>

    <ServerTable
      v-else
      v-bind="serverTableProps"
      v-on="listeners()"
    >
      <template
        v-for="name in tableSlotNames"
        :key="name"
        #[name]="data"
      >
        <slot
          :name="name"
          v-bind="passThrough(data)"
        />
      </template>
    </ServerTable>
  </AppCard>
</template>
