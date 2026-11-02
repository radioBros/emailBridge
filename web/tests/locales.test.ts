import { describe, expect, it } from "vitest";
import {
  LOCALES,
  LOCALE_CODES,
  bcp47Of,
  localeOf,
  resolveLocale,
  vuetifyOf,
} from "@/app/locales";

describe("locales registry", () => {
  it("lists exactly nine locales with required fields", () => {
    expect(LOCALES).toHaveLength(9);
    expect(LOCALE_CODES).toEqual(["en", "it", "es", "fr", "de", "pt", "ru", "zh", "ja"]);
    for (const l of LOCALES) {
      expect(l.nativeName.length).toBeGreaterThan(0);
      expect(l.dir).toBe("ltr");
      expect(l.bcp47.length).toBeGreaterThan(0);
      expect(l.vuetify.length).toBeGreaterThan(0);
    }
  });

  it("maps zh to zhHans and BCP 47 tags", () => {
    expect(vuetifyOf("zh")).toBe("zhHans");
    expect(bcp47Of("pt")).toBe("pt-BR");
    expect(localeOf("nope").code).toBe("en");
  });

  it("resolveLocale order", () => {
    expect(resolveLocale({ saved: "es", stored: "fr", navigatorLanguages: ["de"] })).toBe("es");
    expect(resolveLocale({ stored: "fr", navigatorLanguages: ["de"] })).toBe("fr");
    expect(resolveLocale({ navigatorLanguages: ["de-AT"] })).toBe("de");
  });
});
