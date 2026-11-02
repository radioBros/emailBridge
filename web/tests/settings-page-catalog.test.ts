import { describe, expect, it } from "vitest";
import {
  SETTINGS_FIELDS,
  SETTINGS_TABS,
  valuesEqual,
} from "@/features/settings/catalog";
import enLocale from "@/features/settings/locales/en.json";
import itLocale from "@/features/settings/locales/it.json";

describe("settings catalog", () => {
  it("covers every settings tab except switches", () => {
    const tabs = SETTINGS_TABS.filter((t) => t !== "switches");
    for (const tab of tabs) {
      expect(SETTINGS_FIELDS[tab].length).toBeGreaterThan(0);
    }
  });

  it("marks global-only protocolLogs.rawDebugTtl", () => {
    const field = SETTINGS_FIELDS.security.find((f) => f.key === "protocolLogs.rawDebugTtl");
    expect(field?.changeScope).toBe("global");
  });

  it("compares draft values deeply", () => {
    expect(valuesEqual(["a", "b"], ["a", "b"])).toBe(true);
    expect(valuesEqual(["a"], ["b"])).toBe(false);
    expect(valuesEqual(1, "1")).toBe(true);
    expect(valuesEqual(null, undefined)).toBe(true);
  });

  it("every setting field has a translated label (never equals its raw key)", () => {
    const keys = Object.values(SETTINGS_FIELDS).flatMap((fields) =>
      fields.map((f) => f.key),
    );
    for (const [tag, locale] of [
      ["en", enLocale],
      ["it", itLocale],
    ] as const) {
      const bag = locale.settings.keys as Record<string, string>;
      for (const key of keys) {
        const label = bag[key];
        expect(label, `${tag} missing label for ${key}`).toBeTruthy();
        expect(label, `${tag} label equals key for ${key}`).not.toBe(key);
      }
    }
  });
});
