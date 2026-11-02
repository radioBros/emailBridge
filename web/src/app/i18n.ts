import { createI18n } from "vue-i18n";
import {
  DEFAULT_LOCALE,
  isLocale,
  readStoredLocale,
  resolveLocale,
  setDocumentLang,
  writeStoredLocale,
  vuetifyOf,
} from "@/app/locales";

type MessageTree = Record<string, unknown>;

const deepMerge = (target: MessageTree, source: MessageTree): MessageTree => {
  const out: MessageTree = { ...target };
  for (const [key, value] of Object.entries(source)) {
    if (
      value &&
      typeof value === "object" &&
      !Array.isArray(value) &&
      typeof out[key] === "object" &&
      out[key] !== null &&
      !Array.isArray(out[key])
    ) {
      out[key] = deepMerge(out[key] as MessageTree, value as MessageTree);
    } else {
      out[key] = value;
    }
  }
  return out;
};

/** Eager English (fallback) — always in the main bundle. */
const enShared = import.meta.glob("../lang/en/*.json", {
  eager: true,
  import: "default",
});
const enFeatures = import.meta.glob("../features/*/locales/en.json", {
  eager: true,
  import: "default",
});

/** Lazy loaders for every locale folder (including en for parity of the glob shape). */
const sharedGlob = import.meta.glob("../lang/*/*.json");
const featureGlob = import.meta.glob("../features/*/locales/*.json");

const mergeEager = (
  shared: Record<string, unknown>,
  features: Record<string, unknown>,
): MessageTree => {
  let messages: MessageTree = {};
  for (const mod of Object.values(shared)) {
    messages = deepMerge(messages, mod as MessageTree);
  }
  for (const mod of Object.values(features)) {
    messages = deepMerge(messages, mod as MessageTree);
  }
  return messages;
};

const loadLocaleMessages = async (locale: string): Promise<MessageTree> => {
  if (!isLocale(locale)) return {};
  let messages: MessageTree = {};
  const sharedPrefix = `/lang/${locale}/`;
  const featureSuffix = `/locales/${locale}.json`;
  await Promise.all([
    ...Object.entries(sharedGlob).map(async ([path, loader]) => {
      if (!path.includes(sharedPrefix)) return;
      const mod = (await loader()) as { default?: MessageTree } | MessageTree;
      const tree = (mod as { default?: MessageTree }).default ?? (mod as MessageTree);
      messages = deepMerge(messages, tree);
    }),
    ...Object.entries(featureGlob).map(async ([path, loader]) => {
      if (!path.endsWith(featureSuffix)) return;
      const mod = (await loader()) as { default?: MessageTree } | MessageTree;
      const tree = (mod as { default?: MessageTree }).default ?? (mod as MessageTree);
      messages = deepMerge(messages, tree);
    }),
  ]);
  return messages;
};

const enMessages = mergeEager(enShared, enFeatures);

const i18n = createI18n({
  legacy: false,
  locale: DEFAULT_LOCALE,
  fallbackLocale: DEFAULT_LOCALE,
  messages: { en: enMessages } as never,
}) as unknown as {
  install: (app: unknown) => void;
  global: {
    t: (key: string, named?: Record<string, unknown>) => string;
    te: (key: string) => boolean;
    locale: { value: string };
    setLocaleMessage: (locale: string, messages: MessageTree) => void;
    getLocaleMessage: (locale: string) => MessageTree;
    availableLocales: string[];
  };
};

const loaded = new Set<string>([DEFAULT_LOCALE]);

type VuetifyLocaleApi = {
  current: { value: string };
};

let vuetifyLocale: VuetifyLocaleApi | null = null;

/** Called once from the Vuetify plugin so locale switches stay in sync. */
export const bindVuetifyLocale = (api: VuetifyLocaleApi): void => {
  vuetifyLocale = api;
  api.current.value = vuetifyOf(i18n.global.locale.value);
};

const syncSideEffects = (code: string): void => {
  setDocumentLang(code);
  writeStoredLocale(code);
  if (vuetifyLocale) vuetifyLocale.current.value = vuetifyOf(code);
};

/** Ensure messages for `code` are registered, then activate it. */
export const ensureLocale = async (code: string): Promise<string> => {
  const next = isLocale(code) ? code : DEFAULT_LOCALE;
  if (!loaded.has(next)) {
    const messages = await loadLocaleMessages(next);
    i18n.global.setLocaleMessage(next, messages);
    loaded.add(next);
  }
  i18n.global.locale.value = next;
  syncSideEffects(next);
  return next;
};

export interface BootI18nOptions {
  /** From `/v1/me/settings` when already known (signed-in shell). */
  saved?: string | null;
}

/** Resolve + load the active locale before the first paint. */
export const bootI18n = async (opts: BootI18nOptions = {}): Promise<string> => {
  const code = resolveLocale({
    saved: opts.saved,
    stored: readStoredLocale(),
    navigatorLanguages:
      typeof navigator !== "undefined" ? navigator.languages : undefined,
  });
  return ensureLocale(code);
};

export default i18n;
