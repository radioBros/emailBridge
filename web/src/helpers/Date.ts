import { DateTime, Duration } from "luxon";

import { EMPTY_VALUE } from "./Format";

/**
 * The zone every method uses by default.
 *
 * Change this one line per project, or set `DateUtil.defaultZone` once at bootstrap from your own
 * config. It is deliberately NOT read from the browser: a user travelling, or a server rendering an
 * export, must see the same civil day as the record they are looking at.
 */
const DEFAULT_ZONE = "Europe/Rome";

/**
 * Date and time utilities — timezone-aware, reusable across projects.
 *
 * Ported from the reference project's `helpers/Date.js` and its `DateFormat` / `Durations`
 * companions. **The hand-rolled Intl token formatter was not carried over**: it reimplemented, in a
 * hundred lines, what Luxon already does — and Luxon is already a dependency here because the
 * product needs real IANA timezone handling, not the browser's local offset.
 *
 * **Every conversion is anchored to an explicit zone**, defaulting to the application's
 * (`Europe/Rome`), never to the browser's. An HR user travelling, or a server rendering an export,
 * must see the same civil day as the employee whose attendance it is — reading the local zone is how
 * a presence lands on the wrong date.
 *
 * Civil dates (`YYYY-MM-DD`) carry no instant and are handled as-is; instants (`date-time`) are
 * converted into the target zone first.
 *
 * **This formats and shifts; it never decides a domain value.** No hours worked, no balances, no
 * deviations — those come from the API (CLAUDE.md §2).
 */

/** Anything this module accepts as a moment in time. */
export type DateInput = string | number | Date | DateTime | null | undefined;

/** Italian month names, for `formatPeriod`. */
const MONTHS_IT = [
  "Gennaio",
  "Febbraio",
  "Marzo",
  "Aprile",
  "Maggio",
  "Giugno",
  "Luglio",
  "Agosto",
  "Settembre",
  "Ottobre",
  "Novembre",
  "Dicembre",
] as const;

/** Units `humanize` can emit, largest first. */
const HUMANIZE_UNITS = [
  ["years", "a"],
  ["months", "m"],
  ["weeks", "sett"],
  ["days", "g"],
  ["hours", "h"],
  ["minutes", "min"],
  ["seconds", "s"],
] as const;

export default class DateUtil {
  /** The zone every method uses unless one is passed explicitly. */
  static defaultZone: string = DEFAULT_ZONE;

  /* ------------------------------------------------------------------ parsing */

  /** Parses any accepted input into a Luxon `DateTime`, or `null` when it is not a real date. */
  static parse(value: DateInput, zone: string = DateUtil.defaultZone): DateTime | null {
    if (value === null || value === undefined || value === "") return null;

    let parsed: DateTime;
    if (value instanceof DateTime) parsed = value.setZone(zone);
    else if (value instanceof Date) parsed = DateTime.fromJSDate(value, { zone });
    else if (typeof value === "number") parsed = DateTime.fromMillis(value, { zone });
    else parsed = DateTime.fromISO(value, { zone });

    return parsed.isValid ? parsed : null;
  }

  /** Seconds since the epoch → `DateTime`. Unix timestamps arrive from some integrations. */
  static fromUnix(seconds: number, zone: string = DateUtil.defaultZone): DateTime | null {
    return DateUtil.parse(seconds * 1000, zone);
  }

  static isValid(value: DateInput): boolean {
    return DateUtil.parse(value) !== null;
  }

  static isPast(value: DateInput): boolean {
    const parsed = DateUtil.parse(value);
    return parsed !== null && parsed < DateTime.now().setZone(DateUtil.defaultZone);
  }

  static isFuture(value: DateInput): boolean {
    const parsed = DateUtil.parse(value);
    return parsed !== null && parsed > DateTime.now().setZone(DateUtil.defaultZone);
  }

  static isToday(value: DateInput): boolean {
    return DateUtil.toIso(value) === DateUtil.today();
  }

  /* ---------------------------------------------------------------- formatting */

  /** Formats with a Luxon token string. Returns an em-dash for a missing or invalid value. */
  static format(value: DateInput, token = "dd/MM/yyyy"): string {
    return DateUtil.parse(value)?.toFormat(token) ?? EMPTY_VALUE;
  }

  /** `15/03/2026` */
  static formatDate(value: DateInput): string {
    return DateUtil.format(value, "dd/MM/yyyy");
  }

  /** 24-hour wall-clock time in the target zone: `09:12` */
  static formatTime(value: DateInput): string {
    return DateUtil.format(value, "HH:mm");
  }

  /** `15/03/2026 09:12` */
  static formatDateTime(value: DateInput): string {
    return DateUtil.format(value, "dd/MM/yyyy HH:mm");
  }

  /**
   * A `YYYY-MM` period as `Settembre 2026`.
   *
   * The month name comes from the table above rather than `vue-i18n`, because this is called from
   * stores and exports that have no component context. A second locale means routing it through
   * `domain.months.*`.
   */
  static formatPeriod(value: string | null | undefined): string {
    if (!value) return EMPTY_VALUE;
    const parsed = DateTime.fromFormat(value, "yyyy-MM", { zone: DateUtil.defaultZone });
    if (!parsed.isValid) return EMPTY_VALUE;
    return `${MONTHS_IT[parsed.month - 1]} ${parsed.year}`;
  }

  /** Civil `YYYY-MM-DD`, the wire format for a date. */
  static toIso(value: DateInput): string | null {
    return DateUtil.parse(value)?.toISODate() ?? null;
  }

  /** Full ISO instant, the wire format for a `date-time`. */
  static toIsoDateTime(value: DateInput): string | null {
    return DateUtil.parse(value)?.toISO() ?? null;
  }

  /* -------------------------------------------------------------------- "now" */

  /** Today as a civil `YYYY-MM-DD`. */
  static today(): string {
    return DateTime.now().setZone(DateUtil.defaultZone).toISODate() ?? "";
  }

  /** The current `YYYY-MM` period. */
  static currentPeriod(): string {
    return DateTime.now().setZone(DateUtil.defaultZone).toFormat("yyyy-MM");
  }

  /* ------------------------------------------------------------------- shifting */

  private static shift(value: DateInput, amount: Record<string, number>): string | null {
    return DateUtil.parse(value)?.plus(amount).toISODate() ?? null;
  }

  static addDays(value: DateInput, days: number): string | null {
    return DateUtil.shift(value, { days });
  }

  static subDays(value: DateInput, days: number): string | null {
    return DateUtil.shift(value, { days: -days });
  }

  static addMonths(value: DateInput, months: number): string | null {
    return DateUtil.shift(value, { months });
  }

  static subMonths(value: DateInput, months: number): string | null {
    return DateUtil.shift(value, { months: -months });
  }

  static addYears(value: DateInput, years: number): string | null {
    return DateUtil.shift(value, { years });
  }

  /* ------------------------------------------------------------------ boundaries */

  static startOfMonth(value: DateInput): string | null {
    return DateUtil.parse(value)?.startOf("month").toISODate() ?? null;
  }

  static endOfMonth(value: DateInput): string | null {
    return DateUtil.parse(value)?.endOf("month").toISODate() ?? null;
  }

  static startOfYear(value: DateInput): string | null {
    return DateUtil.parse(value)?.startOf("year").toISODate() ?? null;
  }

  static endOfYear(value: DateInput): string | null {
    return DateUtil.parse(value)?.endOf("year").toISODate() ?? null;
  }

  /** Monday-first, per `design-tokens_spec.md` §6. Luxon's week already starts on Monday. */
  static startOfWeek(value: DateInput): string | null {
    return DateUtil.parse(value)?.startOf("week").toISODate() ?? null;
  }

  static endOfWeek(value: DateInput): string | null {
    return DateUtil.parse(value)?.endOf("week").toISODate() ?? null;
  }

  /** ISO week number, Monday-first. */
  static isoWeek(value: DateInput): number | null {
    return DateUtil.parse(value)?.weekNumber ?? null;
  }

  /* ----------------------------------------------------------------- differences */

  /** Whole days from `start` to `end`. Negative when `end` precedes `start`. */
  static diffDays(start: DateInput, end: DateInput): number | null {
    const from = DateUtil.parse(start);
    const to = DateUtil.parse(end);
    if (from === null || to === null) return null;
    return Math.round(to.startOf("day").diff(from.startOf("day"), "days").days);
  }

  /** Absolute seconds between a timestamp and now — the reference project's `secondsDiff`. */
  static secondsSince(value: DateInput): number | null {
    const parsed = DateUtil.parse(value);
    if (parsed === null) return null;
    return Math.abs(Math.floor(DateTime.now().diff(parsed, "seconds").seconds));
  }

  /* ------------------------------------------------------------------- durations */

  /** Seconds → `hh:mm:ss` (or any Luxon duration token), for elapsed-time displays. */
  static duration(seconds: number | null | undefined, token = "hh:mm:ss"): string {
    if (seconds === null || seconds === undefined || Number.isNaN(seconds)) return EMPTY_VALUE;
    return Duration.fromObject({ seconds: Math.abs(Math.trunc(seconds)) })
      .shiftTo("hours", "minutes", "seconds")
      .toFormat(token);
  }

  /**
   * Seconds → a short human string: `2g 4h 30min`.
   *
   * @param seconds  elapsed seconds
   * @param maxUnits how many units to show before stopping — 2 reads well, 7 is a wall of text
   */
  static humanize(seconds: number | null | undefined, maxUnits = 2): string {
    if (seconds === null || seconds === undefined || Number.isNaN(seconds)) return EMPTY_VALUE;

    const total = Math.abs(Math.trunc(seconds));
    if (total === 0) return "0s";

    const parts = Duration.fromObject({ seconds: total })
      .shiftTo("years", "months", "weeks", "days", "hours", "minutes", "seconds")
      .toObject();

    const rendered: string[] = [];
    for (const [unit, suffix] of HUMANIZE_UNITS) {
      const amount = Math.trunc(parts[unit] ?? 0);
      if (amount > 0) rendered.push(`${amount}${suffix}`);
      if (rendered.length === maxUnits) break;
    }

    return rendered.join(" ");
  }
}

/*
 * Function-style re-exports.
 *
 * The class is the reusable surface; these keep call sites reading naturally (`formatDate(x)` rather
 * than `DateUtil.formatDate(x)`) and are what the rest of this application imports.
 */
export const parseDate = (value: DateInput): DateTime | null => DateUtil.parse(value);
export const formatDate = (value: DateInput): string => DateUtil.formatDate(value);
export const formatTime = (value: DateInput): string => DateUtil.formatTime(value);
export const formatDateTime = (value: DateInput): string => DateUtil.formatDateTime(value);
export const formatPeriod = (value: string | null | undefined): string =>
  DateUtil.formatPeriod(value);
export const isoWeek = (value: DateInput): number | null => DateUtil.isoWeek(value);
export const today = (): string => DateUtil.today();
export const currentPeriod = (): string => DateUtil.currentPeriod();
