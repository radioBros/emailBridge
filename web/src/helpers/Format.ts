/**
 * Display formatters — Italian conventions.
 *
 * **These format; they never calculate.** Minutes → `7h 30m` and half-days → `9,5` are unit
 * conversions for display, which is what `frontend-stack_decision.md` §2.8 permits. Anything that
 * decides a value — hours worked, a balance, a deviation — comes from the API.
 *
 * Every function tolerates `null`/`undefined` and renders an em-dash. Missing data is a real state
 * in this product and must never surface as `NaN`.
 */

/** Rendered in place of a missing value. */
export const EMPTY_VALUE = "—";

const isMissing = (value: number | null | undefined): value is null | undefined =>
  value === null || value === undefined || Number.isNaN(value);

/**
 * A number with the Italian comma decimal separator and dot thousands grouping.
 *
 * @example formatNumber(1234.5, 2)  // "1.234,50"
 */
export const formatNumber = (value: number | null | undefined, fractionDigits = 2): string => {
  if (isMissing(value)) return EMPTY_VALUE;

  return new Intl.NumberFormat("it-IT", {
    minimumFractionDigits: fractionDigits,
    maximumFractionDigits: fractionDigits,
  }).format(value);
};

/**
 * Integer minutes as `7h 30m`.
 *
 * Negative durations keep their sign (`-1h 15m`) — a negative balance movement is meaningful and
 * must not render as its absolute value.
 *
 * @example formatDuration(450)  // "7h 30m"
 * @example formatDuration(60)   // "1h"
 * @example formatDuration(0)    // "0m"
 */
export const formatDuration = (minutes: number | null | undefined): string => {
  if (isMissing(minutes)) return EMPTY_VALUE;

  const sign = minutes < 0 ? "-" : "";
  const absolute = Math.abs(Math.trunc(minutes));
  const hours = Math.floor(absolute / 60);
  const remainder = absolute % 60;

  if (hours === 0) return `${sign}${remainder}m`;
  if (remainder === 0) return `${sign}${hours}h`;
  return `${sign}${hours}h ${remainder}m`;
};

/**
 * Integer minutes as decimal hours with an Italian comma separator.
 *
 * @example formatDecimalHours(450)  // "7,50"
 */
export const formatDecimalHours = (
  minutes: number | null | undefined,
  fractionDigits = 2,
): string => {
  if (isMissing(minutes)) return EMPTY_VALUE;
  return formatNumber(minutes / 60, fractionDigits);
};

/**
 * Half-days as a day count: `19` → `9,5`.
 *
 * **Returns the number only.** The unit label is an i18n string (`domain.days`) applied by the
 * caller — `design-tokens_spec.md` §6 shows this as "9,5 giorni", but baking the Italian word into a
 * helper is exactly the drift the i18n-from-day-one rule prevents (CLAUDE.md §3.10). Recorded as a
 * deviation in `frontend-status_log.md` §7.
 */
export const formatDays = (halfDays: number | null | undefined): string => {
  if (isMissing(halfDays)) return EMPTY_VALUE;

  const days = halfDays / 2;
  // Whole days render as "9", not "9,0" — a trailing zero reads as false precision in a balance.
  return Number.isInteger(days) ? formatNumber(days, 0) : formatNumber(days, 1);
};

/**
 * A signed number, always carrying its sign — for balance movements, where `+2` and `-2` must be
 * visually distinct and a bare `2` is ambiguous.
 */
export const formatSigned = (value: number | null | undefined, fractionDigits = 1): string => {
  if (isMissing(value)) return EMPTY_VALUE;
  const formatted = formatNumber(value, fractionDigits);
  return value > 0 ? `+${formatted}` : formatted;
};
