import { mdiCheck, mdiChevronDown, mdiCloseCircle } from "@mdi/js";
import { VAvatar, VBtn, VIcon } from "vuetify/components";
import { DIALOG_SHADOW_CLASS, OVERLAY_SHADOW_CLASS } from "@/core/config/UiLayout";

interface DataTableRow {
  item?: Record<string, unknown>;
}

const rowIdentity = ({ item }: DataTableRow) => {
  const id = String(item?.id ?? item?.uuid ?? "");
  return {
    "data-id": id,
    "data-testid": `row-${id}`,
  };
};

/**
 * Vuetify component defaults — shape, colour and overlay chrome.
 *
 * Knobs (change here → applies app-wide):
 * - `global.rounded` / per-component `rounded` — corner radius step (values in settings.scss; 12 px)
 * - Field `bgColor: "field"` — white (light) / dark surface tone (dark); see VuetifyThemes
 * - Overlay `contentClass: OVERLAY_SHADOW_CLASS` — soft shadow on menus / dialogs / sheets / tooltips
 * - `VSkeletonLoader.color` — skeleton bone colour token
 * - Density / variant defaults for buttons, chips, alerts
 * - Size / density (W67): NO element carries `density=` or `size=`. Every input takes
 *   `global.density`, so all fields in the app share one height (48 px). A category that genuinely
 *   needs another size gets a nested default below (buttons in alerts / table rows / list rows,
 *   app-bar icons, timelines) or one of the role aliases in `componentAliases` (`<v-btn-inline>`,
 *   `<v-avatar-hero>`, `<v-icon-inline>` …). Never put the attribute back on an element.
 *
 * Do not scatter per-page overrides for these. Icons inside buttons keep the button colour.
 */

// Base button chrome. Aliases spread it explicitly: a Vuetify alias reads ONLY its own key, so
// without the spread `<v-btn-inline>` would lose tonal / radius / ripple.
const btnBase = {
  variant: "tonal",
  rounded: "lg",
  ripple: true,
  size: "large",
  density: "default",
} as const;

// Avatars are tonal discs; the icon inside follows the disc size (role aliases below).
const avatarBase = { variant: "tonal" } as const;

// Row / list-row actions: icon buttons 40 px, text buttons 28 px — a row stays one line high.
const rowActions = {
  VBtn: { size: "small" },
} as const;

const defaults = {
  global: {
    // `lg` = 12 px in settings.scss — one radius drives buttons, fields, cards, menus, dialogs…
    rounded: "lg",
    density: "comfortable",
  },

  VBtn: btnBase,

  // ── Button role aliases (registered via `componentAliases`) ──────────────────────────────
  // Compact action inside content: section-header "add" buttons, add/remove-row actions in forms,
  // inline edit/delete on a card row, the message reader toolbar and the bulk-selection strip.
  VBtnInline: { ...btnBase, size: "small" },
  // Tiny icon stuck to a value (copy an ID, inline edit pencil): 28 px, same as the dialog close.
  VBtnAffix: { ...btnBase, size: "small", density: "comfortable" },
  // Icon button beside an input in a toolbar (column manager, sort order): 48 px = the field
  // height, so the toolbar reads as one line.
  VBtnFieldIcon: { ...btnBase, size: "default" },
  // Formatting-toolbar button (rich-text editor): dense row of many icon tools above a field.
  VBtnTool: { ...btnBase, size: "x-small" },

  // VCardActions hard-codes variant:"text" on nested buttons; restore tonal so dialog
  // / card footers match the rest of the app (destructive stays primary via colour).
  VCardActions: {
    VBtn: {
      variant: "tonal",
    },
  },

  // Segmented controls (login MFA, settings scope, route match/mode). `default` density = 48 px,
  // the field height, because they sit in forms and filter bars next to inputs.
  VBtnToggle: {
    variant: "outlined",
    density: "default",
  },

  // Top-bar chrome (menu, locale, theme, avatar): text so they read as icons, not CTAs.
  // Comfortable = 44 px icons, one size for every top-bar control; the user disc is 36 px.
  VAppBar: {
    VBtn: {
      variant: "text",
      density: "comfortable",
    },
    VAvatar: { size: 36 },
    // Organisation picker in the 56 px bar: compact (40 px) so its floating label is not clipped.
    // The only input outside page content — every page / dialog input keeps the global height.
    VSelect: { density: "compact" },
  },

  // Snackbar action icons sit on a coloured bar — text avoids a second filled chip; small so the
  // actions fit the bar. (rounded lives here too; do not split VSnackbar across two keys.)
  VSnackbar: {
    rounded: "lg",
    transition: "fade-transition",
    VBtn: {
      variant: "text",
      size: "small",
    },
  },

  // Status / identity discs — tonal matches chips/alerts; change here app-wide.
  // Default disc = 40 px (Vuetify default): page-title and card-header badges, dialog headers.
  VAvatar: {
    ...avatarBase,
    VIcon: { size: 22 },
  },
  // ── Avatar role aliases ──────────────────────────────────────────────────────────────────
  // Centred empty / permission / success state illustration.
  VAvatarHero: { ...avatarBase, size: 56, VIcon: { size: 28 } },
  // Leading disc of a horizontal status row (load error + retry) and auth card headers.
  VAvatarLead: { ...avatarBase, size: 48, VIcon: { size: 26 } },
  // Small disc in a dense panel or table row (test result, audit actor, health severity).
  VAvatarSmall: { ...avatarBase, size: 32, VIcon: { size: 18 } },

  // ── Icon role aliases ────────────────────────────────────────────────────────────────────
  // Icon inside a line of text or a table cell (lock badge, flag, attachment, drag handle).
  VIconInline: { size: 18 },
  // Pass / fail badge beside a result heading (verify results).
  VIconStatus: { size: 28 },
  // Lone success mark of a finished flow (hosted connect page).
  VIconHero: { size: 48 },

  VField: {
    VIcon: {
      color: "primary",
    },
  },

  // `field` is white in light mode and the dark card surface in dark mode (never a light plate).
  VTextField: {
    variant: "outlined",
    color: "primary",
    bgColor: "field",
    rounded: "lg",
    clearIcon: mdiCloseCircle,
  },
  VTextarea: {
    variant: "outlined",
    color: "primary",
    bgColor: "field",
    rounded: "lg",
    clearIcon: mdiCloseCircle,
  },
  VNumberInput: {
    variant: "outlined",
    color: "primary",
    bgColor: "field",
    rounded: "lg",
    clearIcon: mdiCloseCircle,
  },
  VSelect: {
    variant: "outlined",
    color: "primary",
    bgColor: "field",
    rounded: "lg",
    menuIcon: mdiChevronDown,
    clearIcon: mdiCloseCircle,
    menuProps: { contentClass: OVERLAY_SHADOW_CLASS, rounded: "lg" },
    // AutocompleteSearch menu search field (kit prepend-item) — compact, not page density.
    VTextField: {
      density: "compact",
      variant: "outlined",
    },
  },
  VAutocomplete: {
    variant: "outlined",
    color: "primary",
    bgColor: "field",
    rounded: "lg",
    menuIcon: mdiChevronDown,
    clearIcon: mdiCloseCircle,
    menuProps: { contentClass: OVERLAY_SHADOW_CLASS, rounded: "lg" },
  },
  VCombobox: {
    variant: "outlined",
    color: "primary",
    bgColor: "field",
    rounded: "lg",
    menuIcon: mdiChevronDown,
    clearIcon: mdiCloseCircle,
    menuProps: { contentClass: OVERLAY_SHADOW_CLASS, rounded: "lg" },
  },

  VForm: {
    validateOn: "input",
  },

  VCard: {
    rounded: "lg",
    flat: true,
    border: true,
  },
  // Owner preference: overlays fade (no scale / slide). The bottom sheet keeps its slide-up.
  VDialog: {
    rounded: "lg",
    contentClass: DIALOG_SHADOW_CLASS,
    transition: "fade-transition",
  },
  VBottomSheet: {
    rounded: "lg",
    contentClass: DIALOG_SHADOW_CLASS,
  },
  VMenu: {
    rounded: "lg",
    contentClass: OVERLAY_SHADOW_CLASS,
    transition: "fade-transition",
  },

  VChip: {
    // Named step in settings.scss (= 12 px); not pill unless a call site opts in.
    rounded: "chip",
    variant: "tonal",
    // One chip size app-wide (status, type and count chips in tables, cards, drawers).
    size: "small",
  },

  // Alert actions (retry, apply preset) are small so the alert keeps its one-line height.
  VAlert: {
    variant: "tonal",
    rounded: "lg",
    VBtn: { size: "small" },
  },

  // Timelines (account events, delivery attempts, PEC receipts) are dense side content: compact
  // rows, x-small dots, x-small chips and small inline actions.
  VTimeline: {
    density: "compact",
    size: "x-small",
    VChip: { size: "x-small" },
    VBtn: { size: "small" },
  },

  // Mobile card list: the end-of-list loading spinner is section scale.
  VInfiniteScroll: {
    VProgressCircular: { size: 24 },
  },

  VSkeletonLoader: {
    color: "skeleton",
  },

  // Eager tooltips leave empty role=tooltip overlays in the DOM (axe aria-tooltip-name).
  VTooltip: {
    eager: false,
    contentClass: OVERLAY_SHADOW_CLASS,
  },

  VCheckbox: { color: "primary" },
  VCheckboxBtn: { color: "primary" },
  VRadioGroup: { color: "primary" },
  VRadio: { color: "primary" },
  VSlider: { color: "primary" },
  VRangeSlider: { color: "primary" },
  // Named for axe aria-progressbar-name; call sites may override with i18n.
  VProgressCircular: { color: "primary", "aria-label": "Loading" },
  VProgressLinear: { color: "primary", "aria-label": "Loading" },
  VTabs: { color: "primary" },
  // Every window (tabs, stepper, settings) crossfades instead of sliding: a slide translates the
  // outgoing panel sideways, and with `.v-window { overflow: visible }` (main.scss — keeps focused
  // field labels from being clipped) that panel would widen the page during the transition.
  // Crossfade takes the outgoing panel out of flow, so nothing moves sideways or stacks.
  VWindow: { crossfade: true },
  VTabsWindow: { crossfade: true },
  VStepperWindow: { crossfade: true },
  // Page buttons: tonal squares with `md` (8 px) radius — never circles / pills.
  // Active = tonal primary (tint + primary digit); overlay wash fixed in `_tables.scss`.
  // Fixed size keeps first/prev/page/next/last equal width and height for single digits.
  VPagination: {
    activeColor: "primary",
    variant: "tonal",
    rounded: "md",
    size: 36,
    // Compact keeps the footer bar one line on a phone (TableFooter is the only pagination).
    density: "compact",
  },

  VSwitch: {
    color: "primary",
    trueIcon: mdiCheck,
    falseIcon: mdiCloseCircle,
    inset: true,
  },

  // List rows: `#append` actions follow the table-row size; tag chips beside a title are x-small.
  VListItem: {
    rounded: "lg",
    ...rowActions,
    VChip: { size: "x-small" },
  },

  // Collapsed admin rail: 60 px so icons stay centred when the list overflows.
  VNavigationDrawer: { railWidth: 60 },

  // W66: DateRangeField clear — small text icon beside the activator (not a large
  // tonal block). Applied via <v-defaults-provider> in DateRangeField.vue only.
  DateRangeField: {
    VBtn: {
      variant: "text",
      size: "small",
      density: "comfortable",
      rounded: "lg",
    },
  },

  // W71: dialog / drawer header X — compact text icon (not a large tonal CTA).
  AppDialogClose: {
    VBtn: {
      variant: "text",
      size: "small",
      density: "comfortable",
      rounded: "lg",
    },
  },

  // Kit bug (b): rowProps under VDataTable never reached VDataTableVirtual.
  VDataTable: { rowProps: rowIdentity },
  VDataTableVirtual: { rowProps: rowIdentity },
  // Row action buttons (edit / delete / test / menu) are small: desktop rows here (keyed on the
  // row, so a table's empty-state CTA keeps the normal size), the phone card list through the
  // `CardList` key (applied with <v-defaults-provider> in CardList.vue).
  VDataTableRow: rowActions,
  CardList: rowActions,
} as const;

/**
 * Role aliases: `<v-btn-inline>`, `<v-avatar-hero>` … are the base component under a role name,
 * sized by the matching key above. Registered in `createVuetify({ aliases })` — the app plugin
 * and `tests/setup.ts`.
 */
export const componentAliases = {
  VBtnInline: VBtn,
  VBtnAffix: VBtn,
  VBtnFieldIcon: VBtn,
  VBtnTool: VBtn,
  VAvatarHero: VAvatar,
  VAvatarLead: VAvatar,
  VAvatarSmall: VAvatar,
  VIconInline: VIcon,
  VIconStatus: VIcon,
  VIconHero: VIcon,
};

export default defaults;
