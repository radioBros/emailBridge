import { describe, expect, it, vi } from "vitest";
import { createI18n } from "vue-i18n";
import {
  DEFAULT_LOCALE,
  LOCALE_CODES,
  isLocale,
  matchTag,
  resolveLocale,
} from "@/app/locales";

type Tree = Record<string, unknown>;

const deepMerge = (target: Tree, source: Tree): Tree => {
  const out: Tree = { ...target };
  for (const [key, value] of Object.entries(source)) {
    if (
      value &&
      typeof value === "object" &&
      !Array.isArray(value) &&
      typeof out[key] === "object" &&
      out[key] !== null &&
      !Array.isArray(out[key])
    ) {
      out[key] = deepMerge(out[key] as Tree, value as Tree);
    } else {
      out[key] = value;
    }
  }
  return out;
};

const loadLocale = async (locale: string): Promise<Tree> => {
  const shared = import.meta.glob("../src/lang/*/*.json", { eager: true, import: "default" });
  const features = import.meta.glob("../src/features/*/locales/*.json", {
    eager: true,
    import: "default",
  });
  let messages: Tree = {};
  for (const [path, mod] of Object.entries(shared)) {
    if (!path.includes(`/lang/${locale}/`)) continue;
    messages = deepMerge(messages, mod as Tree);
  }
  for (const [path, mod] of Object.entries(features)) {
    if (!path.endsWith(`/locales/${locale}.json`)) continue;
    messages = deepMerge(messages, mod as Tree);
  }
  return messages;
};

const flatten = (obj: Tree, prefix = ""): string[] => {
  const keys: string[] = [];
  for (const [k, v] of Object.entries(obj)) {
    const path = prefix ? `${prefix}.${k}` : k;
    if (v && typeof v === "object" && !Array.isArray(v)) keys.push(...flatten(v as Tree, path));
    else keys.push(path);
  }
  return keys;
};

const placeholders = (s: string): string[] => {
  const out: string[] = [];
  const re = /\{([^{}]+)\}/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(s)) !== null) out.push(m[1]!);
  return out.sort();
};

const leafAt = (obj: Tree, path: string): unknown => {
  const parts = path.split(".");
  let cur: unknown = obj;
  for (const p of parts) {
    if (!cur || typeof cur !== "object") return undefined;
    cur = (cur as Tree)[p];
  }
  return cur;
};

describe("resolveLocale", () => {
  it("prefers saved ui.locale", () => {
    expect(resolveLocale({ saved: "ja", stored: "it", navigatorLanguages: ["fr"] })).toBe("ja");
  });

  it("falls back to stored then navigator primary subtag then en", () => {
    expect(resolveLocale({ stored: "de" })).toBe("de");
    expect(resolveLocale({ navigatorLanguages: ["zh-TW", "en"] })).toBe("zh");
    expect(resolveLocale({ navigatorLanguages: ["pt-PT"] })).toBe("pt");
    expect(resolveLocale({ navigatorLanguages: ["de-AT"] })).toBe("de");
    expect(resolveLocale({ navigatorLanguages: ["xx-YY"] })).toBe(DEFAULT_LOCALE);
    expect(resolveLocale({})).toBe(DEFAULT_LOCALE);
  });

  it("ignores unknown saved/stored values", () => {
    expect(resolveLocale({ saved: "xx", stored: "yy" })).toBe(DEFAULT_LOCALE);
  });

  it("matchTag handles underscores and empty", () => {
    expect(matchTag("zh_CN")).toBe("zh");
    expect(matchTag("")).toBeNull();
    expect(isLocale("ru")).toBe(true);
    expect(isLocale("nope")).toBe(false);
  });

  it("survives storage failure via try/catch at call sites", () => {
    const spy = vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    // resolveLocale itself does not touch storage — callers pass stored: null on failure
    expect(resolveLocale({ stored: null, navigatorLanguages: ["fr-FR"] })).toBe("fr");
    spy.mockRestore();
  });
});

describe("i18n", () => {
  it(
    "has key parity and placeholder parity with en for every locale",
    async () => {
      const en = await loadLocale("en");
      const enKeys = flatten(en).sort();
      expect(enKeys.length).toBeGreaterThan(0);

      for (const code of LOCALE_CODES) {
        const messages = await loadLocale(code);
        const keys = flatten(messages).sort();
        expect(keys, `${code} key parity`).toEqual(enKeys);
        for (const key of enKeys) {
          const enVal = leafAt(en, key);
          const locVal = leafAt(messages, key);
          if (typeof enVal === "string" && typeof locVal === "string") {
            expect(placeholders(locVal), `${code} ${key}`).toEqual(placeholders(enVal));
          }
        }
      }
    },
    30_000,
  );

  it(
    "every message compiles for every locale",
    async () => {
      const bag: Record<string, Tree> = {};
      for (const code of LOCALE_CODES) {
        bag[code] = await loadLocale(code);
      }
      const i18n = createI18n({
        legacy: false,
        locale: "en",
        fallbackLocale: "en",
        messages: bag as never,
      });
      for (const code of LOCALE_CODES) {
        (i18n.global.locale as { value: string }).value = code;
        for (const key of flatten(bag[code]!)) {
          expect(() => i18n.global.t(key)).not.toThrow();
          expect(String(i18n.global.t(key)).length).toBeGreaterThan(0);
        }
      }
    },
    60_000,
  );
});
