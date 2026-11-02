import type { ValidationRule } from "@/constants/Validations";
import { combine, required } from "@/constants/Validations";
import type { SettingKind } from "@/features/settings/catalog";
import i18nMod from "@/plugins/i18n";

const i18n = i18nMod as {
  global: { t: (key: string, named?: Record<string, unknown>) => unknown };
};

const t = (key: string): string => String(i18n.global.t(key));

const asString = (value: unknown): string =>
  value === null || value === undefined ? "" : String(value);

const isBlank = (value: unknown): boolean => asString(value).trim() === "";

/** Go-style duration plus S9 day suffix: `30s`, `5m`, `1h30m`, `7d`. */
const DURATION_RE =
  /^(0|(\d+(\.\d+)?(ns|us|µs|μs|ms|s|m|h))+|\d+d)$/i;

/** Rate: `20/s`, `10/min`, optional `,burst=40`. */
const RATE_RE = /^\d+\/(s|m|h|min|sec)(,burst=\d+)?$/i;

/** Binary size: `25MiB`, `5GiB`, `512KiB`, `0`. */
const SIZE_RE = /^(0|\d+(\.\d+)?(B|KiB|MiB|GiB|TiB))$/i;

export const nonNegativeNumber: ValidationRule[] = [
  (value) => {
    if (isBlank(value)) return true;
    const n = Number(asString(value).trim());
    return (Number.isFinite(n) && n >= 0) || t("settings.validation.non_negative");
  },
];

export const durationFormat: ValidationRule[] = [
  (value) =>
    isBlank(value) ||
    DURATION_RE.test(asString(value).trim()) ||
    t("settings.validation.duration"),
];

export const rateFormat: ValidationRule[] = [
  (value) =>
    isBlank(value) ||
    RATE_RE.test(asString(value).trim().replace(/\s+/g, "")) ||
    t("settings.validation.rate"),
];

export const sizeFormat: ValidationRule[] = [
  (value) =>
    isBlank(value) || SIZE_RE.test(asString(value).trim()) || t("settings.validation.size"),
];

export const unitInterval: ValidationRule[] = [
  (value) => {
    if (isBlank(value)) return true;
    const n = Number(asString(value).trim());
    return (Number.isFinite(n) && n >= 0 && n <= 1) || t("settings.validation.unit_interval");
  },
];

/** Keys that must be non-empty (S9 has no default / required when used). */
const REQUIRED_KEYS = new Set(["submitPolicy", "sysmailMode"]);

const SAMPLE_RATE_KEYS = new Set(["sentrySampleRate", "sentryTracesSampleRate"]);

export const rulesForField = (
  kind: SettingKind,
  key: string,
  locked: boolean,
): ValidationRule[] => {
  if (locked) return [];

  const needRequired = REQUIRED_KEYS.has(key) ? required : [];

  switch (kind) {
    case "int":
      return combine(needRequired, nonNegativeNumber);
    case "float":
      return SAMPLE_RATE_KEYS.has(key)
        ? combine(needRequired, unitInterval)
        : combine(needRequired, nonNegativeNumber);
    case "duration":
      return combine(needRequired, durationFormat);
    case "rate":
      return combine(needRequired, rateFormat);
    case "size":
      return combine(needRequired, sizeFormat);
    case "select":
      return combine(needRequired.length ? needRequired : required);
    case "bool":
      return [];
    case "strings":
    case "string":
    case "text":
      return needRequired;
    default:
      return needRequired;
  }
};
