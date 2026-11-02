import type { DisplayBreakpoint } from 'vuetify';

// =========================================================================
// The contract every table view shares: ServerTable (rows), CardList (cards)
// and TableCard (picks one). One props/emits/slots definition, so a consumer
// can swap views without touching its bindings.
// =========================================================================

export interface ServerTableHeader {
  key: string;
  title: string;
  forced?: boolean;
  sortable?: boolean;
  align?: 'start' | 'center' | 'end';
  /** Narrowest a resize can make the column, in px (default: MIN_COLUMN_WIDTH, 64). */
  minWidth?: number | string;
  /** `false`: a fixed column, sized to its content and never resized (actions columns always are). */
  resizable?: boolean;
  [key: string]: unknown;
}

export interface ServerTableSortItem {
  key: string;
  order: 'asc' | 'desc';
}

export interface ServerTableProps<T> {
  headers: ServerTableHeader[];
  items: T[];
  total: number;
  page: number;
  ipp: number;
  sortBy: ServerTableSortItem[];
  /** Stable, unique key the column layout is saved under. */
  tableName: string;
  loading?: boolean;
  /** Row identity field. */
  itemValue?: string;
  searchable?: boolean;
  showColManager?: boolean;
  /** Columns can be resized by dragging a header's right edge (widths persist per user). Rows only. */
  resizable?: boolean;
  /**
   * Body height cap in px. When set, overrides fill-height sizing.
   * CardList also accepts a CSS length string for its scroll container.
   */
  maxHeight?: number | string;
  /**
   * Fill remaining viewport below page chrome (min height from UiLayout).
   * Opt out for tables inside tabs / dialogs / nested panels.
   */
  fillHeight?: boolean;
  /** Seeds the search box once (e.g. from a URL query); later changes flow out via update:search. */
  search?: string;
}

export const SERVER_TABLE_DEFAULTS = {
  loading: false,
  itemValue: 'id',
  searchable: true,
  showColManager: true,
  resizable: true,
  maxHeight: undefined,
  fillHeight: true,
  search: '',
};

export interface ServerTableRow<T> {
  item: T;
}

export type ServerTableEmits<T> = {
  'update:page': [value: number];
  'update:ipp': [value: number];
  'update:sortBy': [value: ServerTableSortItem[]];
  'update:search': [value: string];
  'click:row': [event: Event, row: ServerTableRow<T>];
};

export interface ServerTableCellProps<T> {
  item: T;
  index: number;
  value: unknown;
}

export type ServerTableSlots<T> = {
  toolbar?: () => unknown;
  'no-data'?: () => unknown;
  [cell: `item.${string}`]: (props: ServerTableCellProps<T>) => unknown;
  [header: `header.${string}`]: (props: { column: { key: string | null; title?: string } }) => unknown;
};

// ---- card view --------------------------------------------------------------

/** Cards per row. Divisors of 12, so each maps onto a v-col. */
export type CardGridColumns = 1 | 2 | 3 | 4 | 6;

/**
 * Cards per row from a breakpoint UP (mobile first), like v-col sizes: `{ xs: 1, md: 2, xl: 3 }` is one
 * per row on phones, two from md, three from xl.
 */
export type CardGrid = Partial<Record<DisplayBreakpoint, CardGridColumns>>;

/** How the next page arrives: on reaching the end of the list, or from a "Load more" button. */
export type CardLoadMode = 'scroll' | 'button';

export interface CardListProps<T> extends ServerTableProps<T> {
  loadMode?: CardLoadMode;
  grid?: CardGrid;
  /**
   * Screen box: the list scrolls in a box as tall as the screen below the app bar (measured, minus
   * safe-area insets; floor TABLE_FILL_MIN_HEIGHT), not the space left under the page header — the
   * page scrolls until the header is gone, then the list fills the screen. No fill height. A
   * `maxHeight` still wins. TableCard sets it below the mobile breakpoint.
   */
  screenBox?: boolean;
}

export const DEFAULT_CARD_GRID: CardGrid = { xs: 1, md: 2, xl: 3 };

export const CARD_LIST_DEFAULTS = {
  ...SERVER_TABLE_DEFAULTS,
  loadMode: 'scroll' as CardLoadMode,
  grid: () => DEFAULT_CARD_GRID,
  screenBox: false,
};

export interface CardSlotProps<T> {
  item: T;
  index: number;
  /** The visible columns, in the user's order (ColManager applies to cards too). */
  columns: ServerTableHeader[];
  /** Whether the consumer listens to click:row. */
  clickable: boolean;
  /** Emits click:row for this item; bind it to the card's click. */
  select: (event: Event) => void;
}

export type CardListSlots<T> = ServerTableSlots<T> & {
  /** One card. Default: CardListItem (title with its actions, then label/value rows). */
  card?: (props: CardSlotProps<T>) => unknown;
  /** First-load placeholder for one card; mirror a custom #card's shape. */
  'card-skeleton'?: () => unknown;
};

/** TableCard: the views' slots plus the header bar of the card it renders. */
export type TableCardSlots<T> = CardListSlots<T> & {
  /** The card's header bar (title, actions). Omitted: the card has no header. */
  'card-header'?: () => unknown;
};

export interface TableCardProps<T> extends CardListProps<T> {
  /**
   * Cards whatever the viewport: `true`, or a grid (`{ xs: 1, md: 2, xl: 3 }`) to force cards laid out
   * that way. Off (default): cards only below the mobile breakpoint, laid out by `grid`.
   */
  cards?: boolean | CardGrid;
  /** Below this breakpoint (or px width) the cards show. Default: the app's Vuetify mobileBreakpoint. */
  mobileBreakpoint?: number | DisplayBreakpoint;
}

export const TABLE_CARD_DEFAULTS = {
  ...CARD_LIST_DEFAULTS,
  cards: false,
  mobileBreakpoint: undefined,
};
