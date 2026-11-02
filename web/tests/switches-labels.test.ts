import { describe, expect, it } from "vitest";
import { createI18n } from "vue-i18n";
import enSwitches from "@/lang/en/switches.json";
import {
  SWITCH_CATALOG_NAMES,
  humaniseSwitchName,
  switchDescriptionOf,
  switchLabelOf,
} from "@/helpers/Switches";

type Tree = Record<string, unknown>;

const leafAt = (obj: Tree, path: string): unknown => {
  const parts = path.split(".");
  let cur: unknown = obj;
  for (const p of parts) {
    if (!cur || typeof cur !== "object") return undefined;
    cur = (cur as Tree)[p];
  }
  return cur;
};

describe("humaniseSwitchName", () => {
  it("turns dotted camelCase into sentence case", () => {
    expect(humaniseSwitchName("protocolLogs.enabled")).toBe("Protocol logs enabled");
    expect(humaniseSwitchName("accounts.ignoreCertErrors")).toBe(
      "Accounts ignore cert errors",
    );
    expect(humaniseSwitchName("integrations.sso-authentik")).toBe(
      "Integrations sso authentik",
    );
    expect(humaniseSwitchName("integrations.<connector>")).toBe("Integrations connector");
  });

  it("never returns the raw dotted key for known shapes", () => {
    const raw = "sandbox.recipientPolicy";
    expect(humaniseSwitchName(raw)).not.toBe(raw);
    expect(humaniseSwitchName(raw)).not.toContain(".");
  });
});

describe("switchLabelOf / switchDescriptionOf", () => {
  const i18n = createI18n({
    legacy: false,
    locale: "en",
    messages: { en: enSwitches },
  });
  const t = i18n.global.t as (k: string) => string;
  const te = i18n.global.te as (k: string) => boolean;

  it("returns the translated label", () => {
    expect(switchLabelOf("protocolLogs.enabled", t, te)).toBe("Protocol logs");
    expect(switchLabelOf("sandbox.enabled", t, te)).toBe("Sandbox mode");
  });

  it("falls back to humanising when the key is missing", () => {
    expect(switchLabelOf("future.unknownSwitch", t, te)).toBe("Future unknown switch");
  });

  it("returns the translated description", () => {
    expect(switchDescriptionOf("protocolLogs.enabled", t, te)).toContain("IMAP");
  });

  it("falls back to backend description then empty", () => {
    expect(switchDescriptionOf("future.unknown", t, te, "Backend text")).toBe("Backend text");
    expect(switchDescriptionOf("future.unknown", t, te, "  ")).toBe("");
    expect(switchDescriptionOf("future.unknown", t, te)).toBe("");
  });
});

describe("catalog coverage", () => {
  it("every catalog name has an English label", () => {
    for (const name of SWITCH_CATALOG_NAMES) {
      const label = leafAt(enSwitches as Tree, `switches.${name}.label`);
      expect(label, `missing en label for ${name}`).toBeTruthy();
      expect(typeof label, `en label not string for ${name}`).toBe("string");
      expect(String(label).trim().length, `empty en label for ${name}`).toBeGreaterThan(0);
      expect(label, `en label equals raw name for ${name}`).not.toBe(name);
    }
  });

  it("mirrors catalog.go size (83 switches)", () => {
    expect(SWITCH_CATALOG_NAMES).toHaveLength(83);
  });
});
