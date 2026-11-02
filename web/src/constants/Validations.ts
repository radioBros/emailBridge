// Point this at your own vue-i18n instance. The kit assumes `plugins/i18n.ts` exports it as
// default and that `lang/it/validations.json` is merged into the messages.
// Loose import: vue-i18n message types are too deep for vue-tsc.
import i18nMod from "@/plugins/i18n";
const i18n = i18nMod as {
  global: { t: (key: string, named?: Record<string, unknown>) => unknown };
};

/**
 * Centralised Vuetify field validation rules.
 *
 * **Every field that is required, or that has a format, carries `:rules`** — and the rules come from
 * here, never from an inline arrow at the call site. Two reasons: the message wording stays in one
 * place (and in `vue-i18n`, so it is translatable), and "what counts as a valid codice fiscale" is
 * answered once for the whole product rather than per screen.
 *
 * Ported from the reference project's `constants/Validations.js`. The EU-VAT machinery it carried is
 * dropped — it depended on a `vat_regex` shipped by another product's app-config endpoint and has no
 * meaning here (`decisions/frontend-stack_decision.md` §3). Codice fiscale is kept, because an
 * Italian HR product genuinely needs it.
 *
 * **These validate shape, never domain truth.** A rule may check that a date parses; it may not
 * decide whether a leave request fits a balance — that is the server's job (CLAUDE.md §2).
 */

/** A Vuetify rule: `true` when valid, otherwise the message to show. */
export type ValidationRule = (value: unknown) => true | string;

/** Resolved lazily, inside each rule, so the locale is ready before a message is built. */
const t = (key: string, named?: Record<string, unknown>): string =>
  String(named ? i18n.global.t(key, named) : i18n.global.t(key));

const asString = (value: unknown): string => (value === null || value === undefined ? "" : String(value));

const isBlank = (value: unknown): boolean => asString(value).trim() === "";

/** Parses `YYYY-MM-DD` as a civil date; anything else falls back to `Date` parsing. */
const parseLocalDate = (value: unknown): Date => {
  if (value instanceof Date) return value;
  const raw = asString(value);
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(raw);
  if (match) return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
  return new Date(raw);
};

const MEGABYTE = 1024 * 1024;

/*
 * Every rule below passes on an empty value. That is deliberate: "is this filled in" is `required`'s
 * job alone, so a format rule composed with `required` produces one message rather than two, and an
 * optional field with a format does not shout at an empty box.
 */

/** The field must have a value. Compose it with any format rule. */
export const required: ValidationRule[] = [
  (value) => {
    if (Array.isArray(value)) return value.length > 0 || t("validations.requiredField");
    if (typeof value === "boolean") return value || t("validations.requiredField");
    return !isBlank(value) || t("validations.requiredField");
  },
];

export const email: ValidationRule[] = [
  (value) =>
    isBlank(value) ||
    /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(asString(value).trim()) ||
    t("validations.invalidEmail"),
];

/**
 * Password policy.
 *
 * The 12-character minimum is the contract's (`LoginRequest.password.minLength`), not a preference —
 * relaxing it here would let the client accept a password the server rejects.
 */
export const password: ValidationRule[] = [
  (value) => isBlank(value) || asString(value).length >= 12 || t("validations.passwordFormat"),
];

export const onlyNumbers: ValidationRule[] = [
  (value) => isBlank(value) || /^-?\d+$/.test(asString(value).trim()) || t("validations.onlyNumbers"),
];

export const positiveInteger: ValidationRule[] = [
  (value) =>
    isBlank(value) || /^\d+$/.test(asString(value).trim()) || t("validations.positiveInteger"),
];

export const date: ValidationRule[] = [
  (value) =>
    isBlank(value) || !Number.isNaN(parseLocalDate(value).getTime()) || t("validations.invalidDate"),
];

/** Rejects a future date — for a birth date, a hire date, the start of a past absence. */
export const dateNotFuture: ValidationRule[] = [
  (value) => {
    if (isBlank(value)) return true;
    const parsed = parseLocalDate(value);
    if (Number.isNaN(parsed.getTime())) return t("validations.invalidDate");

    const endOfToday = new Date();
    endOfToday.setHours(23, 59, 59, 999);
    return parsed <= endOfToday || t("validations.dateNotFuture");
  },
];

/**
 * Italian codice fiscale — 16 characters, or the 11-digit numeric form some entities use.
 *
 * Shape only. The check character is not verified here: a client that rejects a real CF because of a
 * checksum edge case is worse than one that lets the server have the final say.
 */
export const codiceFiscale: ValidationRule[] = [
  (value) => {
    if (isBlank(value)) return true;
    const normalised = asString(value).trim().toUpperCase();
    const isPersonal = /^[A-Z]{6}\d{2}[A-Z]\d{2}[A-Z]\d{3}[A-Z]$/.test(normalised);
    const isNumeric = /^\d{11}$/.test(normalised);
    return isPersonal || isNumeric || t("validations.invalidCodiceFiscale");
  },
];

/** Italian phone number, with or without an international prefix. */
export const phone: ValidationRule[] = [
  (value) =>
    isBlank(value) ||
    /^\+?[\d\s./-]{6,20}$/.test(asString(value).trim()) ||
    t("validations.invalidPhone"),
];

export const url: ValidationRule[] = [
  (value) => {
    if (isBlank(value)) return true;
    try {
      new URL(asString(value).trim());
      return true;
    } catch {
      return t("validations.invalidUrl");
    }
  },
];

/** Caps the length of a text field. */
export const maxLength = (max: number): ValidationRule[] => [
  (value) => isBlank(value) || asString(value).length <= max || t("validations.maxLength", { max }),
];

export const minLength = (min: number): ValidationRule[] => [
  (value) => isBlank(value) || asString(value).length >= min || t("validations.minLength", { min }),
];

/** Caps upload size. Accepts a single `File` or an array of them. */
export const maxFileSize = (megabytes: number): ValidationRule[] => [
  (value) => {
    if (!value) return true;
    const limit = megabytes * MEGABYTE;
    const files = Array.isArray(value) ? (value as File[]) : [value as File];
    const tooBig = files.some((file) => file instanceof File && file.size > limit);
    return !tooBig || t("validations.maxFileSize", { max: megabytes });
  },
];

/** Restricts an upload to PDF — medical certificates and payslips. */
export const pdfOnly: ValidationRule[] = [
  (value) => {
    if (!value) return true;
    const files = Array.isArray(value) ? (value as File[]) : [value as File];
    const allPdf = files.every(
      (file) => !(file instanceof File) || file.type === "application/pdf",
    );
    return allPdf || t("validations.onlyPdf");
  },
];

/** An arbitrary pattern, for the rare field a named rule does not cover. */
export const matches = (pattern: RegExp, messageKey = "validations.invalidValue"): ValidationRule[] => [
  (value) => isBlank(value) || pattern.test(asString(value)) || t(messageKey),
];

/**
 * Composes rule sets: `combine(required, email)`.
 *
 * Vuetify takes a flat array, and this is the sanctioned way to build one — spreading arrays inline
 * at a call site works too, but tends to drift into a one-off arrow being appended.
 */
export const combine = (...sets: ValidationRule[][]): ValidationRule[] => sets.flat();

const Validations = {
  required,
  email,
  password,
  onlyNumbers,
  positiveInteger,
  date,
  dateNotFuture,
  codiceFiscale,
  phone,
  url,
  maxLength,
  minLength,
  maxFileSize,
  pdfOnly,
  matches,
  combine,
};

export default Validations;
