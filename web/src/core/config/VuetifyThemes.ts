import type { ThemeDefinition } from "vuetify";

/**
 * EmailBridge colourful palette (S13 §2.1 + W61 polish).
 * Keep in sync with `styles/imports/_tokens.scss`. Contrast checked by unit test.
 *
 * Knobs (change here → applies app-wide):
 * - Semantic colours: primary / secondary / accent / success / info / warning / error
 * - Surfaces: background, surface, surface-bright, surface-variant
 * - `skeleton` / `skeleton-highlight` — LoadingSkeleton, SkeletonGrid, VSkeletonLoader bones
 * - `field` — text/select/autocomplete field background (white light / dark surface tone)
 * - `icon-*` — nav and feature icon palette (see IconColors.ts)
 *
 * Dark theme uses 300–400 shades so coloured text / tonal buttons stay ≥ 4.5:1 on
 * the dark surface; `on-*` stays dark so flat fills remain readable.
 * Icon palette tokens target ≥ 3:1 on surface (large icons).
 */
export const light: ThemeDefinition = {
  dark: false,
  colors: {
    background: "#F5F6FB",
    surface: "#FFFFFF",
    "surface-bright": "#FFFFFF",
    "surface-variant": "#E8EAF6",
    // Darker muted text so medium-emphasis (≥ ~0.74 opacity) stays ≥ 4.5:1 on
    // surface and surface-variant (connect / invite subtitles, chip labels).
    "on-surface-variant": "#3F475A",
    // AppCard / AppDialog header bar (grey plate in light mode).
    "card-header": "#E8EAF6",
    "on-card-header": "#1E2433",

    primary: "#4338CA",
    "primary-darken-1": "#3730A3",
    "on-primary": "#FFFFFF",
    // Slightly darker than before so tonal secondary chips clear AA.
    secondary: "#0B6A80",
    "on-secondary": "#FFFFFF",
    accent: "#A21CAF",
    "on-accent": "#FFFFFF",

    success: "#166534",
    "on-success": "#FFFFFF",
    info: "#0369A1",
    "on-info": "#FFFFFF",
    warning: "#9A3412",
    "on-warning": "#FFFFFF",
    error: "#B91C1C",
    "on-error": "#FFFFFF",

    "on-background": "#1E2433",
    "on-surface": "#1E2433",

    // Soft light-grey skeleton bones (not dark).
    skeleton: "#E4E7F0",
    "skeleton-highlight": "#F3F4F9",
    // Field plate: white on light surfaces.
    field: "#FFFFFF",

    // Icon palette (≥ 3:1 on surface) — coherent, not garish.
    "icon-indigo": "#4338CA",
    "icon-cyan": "#0B6A80",
    "icon-violet": "#7C3AED",
    "icon-teal": "#0F766E",
    "icon-amber": "#B45309",
    "icon-rose": "#BE123C",
  },
  variables: {
    "border-color": "#D8DCE8",
    "border-opacity": 1,
    // Default 0.60 fails WCAG AA for body-small on surface-variant (~3.9:1).
    "medium-emphasis-opacity": 0.78,
    "high-emphasis-opacity": 0.92,
    // Soft overlay shadow (menus / dialogs) — mirror of --overlay-shadow in _tokens.
    "overlay-shadow":
      "0 24px 48px -12px rgb(15 23 42 / 0.12), 0 6px 16px -6px rgb(15 23 42 / 0.06)",
  },
};

export const dark: ThemeDefinition = {
  dark: true,
  colors: {
    background: "#0B1020",
    surface: "#121933",
    "surface-bright": "#1A2344",
    "surface-variant": "#1A2344",
    "on-surface-variant": "#C5CCE0",
    "card-header": "#1A2344",
    "on-card-header": "#E8ECF8",

    // Lighter 300–400 shades for text / tonal on dark surfaces
    primary: "#A5B4FC",
    "primary-darken-1": "#818CF8",
    "on-primary": "#1E1B4B",
    secondary: "#67E8F9",
    "on-secondary": "#083344",
    accent: "#F0ABFC",
    "on-accent": "#4A044E",

    success: "#86EFAC",
    "on-success": "#052E16",
    info: "#7DD3FC",
    "on-info": "#0C4A6E",
    warning: "#FCD34D",
    "on-warning": "#451A03",
    error: "#FCA5A5",
    "on-error": "#450A0A",

    "on-background": "#E8ECF8",
    "on-surface": "#E8ECF8",

    // Muted tone a little lighter than the card surface — must not glow.
    skeleton: "#1A2344",
    "skeleton-highlight": "#222B4E",
    // Match dark card surface so fields never look like a light plate inside dark cards.
    field: "#121933",

    "icon-indigo": "#A5B4FC",
    "icon-cyan": "#67E8F9",
    "icon-violet": "#C4B5FD",
    "icon-teal": "#5EEAD4",
    "icon-amber": "#FCD34D",
    "icon-rose": "#FDA4AF",
  },
  variables: {
    "border-color": "#2A3358",
    "border-opacity": 1,
    "medium-emphasis-opacity": 0.78,
    "high-emphasis-opacity": 0.92,
    "overlay-shadow":
      "0 24px 48px -12px rgb(0 0 0 / 0.42), 0 6px 16px -6px rgb(0 0 0 / 0.22)",
  },
};
