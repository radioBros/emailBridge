/** Supported UI locales — single source of truth for the admin SPA. */

export interface Locale {
  code: string;
  nativeName: string;
  vuetify: string;
  bcp47: string;
  dir: "ltr";
}

export const LOCALES: readonly Locale[] = [
  { code: "en", nativeName: "English", vuetify: "en", bcp47: "en", dir: "ltr" },
  { code: "it", nativeName: "Italiano", vuetify: "it", bcp47: "it-IT", dir: "ltr" },
  { code: "es", nativeName: "Español", vuetify: "es", bcp47: "es-ES", dir: "ltr" },
  { code: "fr", nativeName: "Français", vuetify: "fr", bcp47: "fr-FR", dir: "ltr" },
  { code: "de", nativeName: "Deutsch", vuetify: "de", bcp47: "de-DE", dir: "ltr" },
  { code: "pt", nativeName: "Português (Brasil)", vuetify: "pt", bcp47: "pt-BR", dir: "ltr" },
  { code: "ru", nativeName: "Русский", vuetify: "ru", bcp47: "ru-RU", dir: "ltr" },
  { code: "zh", nativeName: "简体中文", vuetify: "zhHans", bcp47: "zh-CN", dir: "ltr" },
  { code: "ja", nativeName: "日本語", vuetify: "ja", bcp47: "ja-JP", dir: "ltr" },
] as const;

export const LOCALE_CODES = LOCALES.map((l) => l.code);

export const DEFAULT_LOCALE = "en";

export const LOCALE_STORAGE_KEY = "eb.ui.locale";

const byCode = new Map(LOCALES.map((l) => [l.code, l]));

export const isLocale = (v: unknown): v is string =>
  typeof v === "string" && byCode.has(v);

export const localeOf = (code: string): Locale =>
  byCode.get(code) ?? byCode.get(DEFAULT_LOCALE)!;

export const bcp47Of = (code: string): string => localeOf(code).bcp47;

export const vuetifyOf = (code: string): string => localeOf(code).vuetify;

/** Match a BCP 47 / Accept-Language tag to a supported short code by primary subtag. */
export const matchTag = (tag: string): string | null => {
  const raw = tag.trim().toLowerCase().replace(/_/g, "-");
  if (!raw) return null;
  const primary = raw.split("-")[0] ?? "";
  if (isLocale(primary)) return primary;
  return null;
};

export interface ResolveLocaleInput {
  /** Signed-in user's `settings.ui.locale`. */
  saved?: string | null;
  /** Last choice from localStorage (caller may pass the raw value). */
  stored?: string | null;
  /** `navigator.languages` or equivalent. */
  navigatorLanguages?: readonly string[] | null;
}

/**
 * Resolution order: saved ui.locale → localStorage → navigator.languages → en.
 */
export const resolveLocale = (input: ResolveLocaleInput = {}): string => {
  if (isLocale(input.saved)) return input.saved;
  if (isLocale(input.stored)) return input.stored;
  const langs = input.navigatorLanguages;
  if (langs) {
    for (const tag of langs) {
      const hit = matchTag(tag);
      if (hit) return hit;
    }
  }
  return DEFAULT_LOCALE;
};

export const readStoredLocale = (): string | null => {
  try {
    const raw = localStorage.getItem(LOCALE_STORAGE_KEY);
    return isLocale(raw) ? raw : null;
  } catch {
    return null;
  }
};

export const writeStoredLocale = (code: string): void => {
  try {
    if (isLocale(code)) localStorage.setItem(LOCALE_STORAGE_KEY, code);
  } catch {
    /* private mode / quota */
  }
};

export const setDocumentLang = (code: string): void => {
  try {
    if (typeof document !== "undefined") {
      document.documentElement.lang = isLocale(code) ? code : DEFAULT_LOCALE;
    }
  } catch {
    /* ignore */
  }
};
