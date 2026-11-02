/**
 * Layout knobs used by shared table / shell chrome.
 *
 * Knobs (change here → applies app-wide):
 * - `TABLE_FILL_MIN_HEIGHT` — floor of a fill-height box (px); the chrome above it is measured
 *   (composables/useFillHeight), not configured
 * - `OVERLAY_SHADOW_CLASS` — contentClass for menus / tooltips (touch more presence)
 * - `DIALOG_SHADOW_CLASS` — softer, wider shadow for dialogs / sheets (scrim already separates)
 */

/**
 * Minimum height of a fill-height table area so a short page still shows a proper table. Below
 * it the page scrolls instead of squashing the table. Same value as `--table-fill-min`.
 */
export const TABLE_FILL_MIN_HEIGHT = 360;

/** Soft overlay shadow recipe — defined in `styles/imports/_tokens.scss`. */
export const OVERLAY_SHADOW_CLASS = "eb-overlay-shadow";

/** Softer dialog/sheet shadow (scrim does the separation; avoid a hard edge). */
export const DIALOG_SHADOW_CLASS = "eb-dialog-shadow";
