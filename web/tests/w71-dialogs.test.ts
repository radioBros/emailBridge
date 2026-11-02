import { describe, expect, it } from "vitest";
import { dark, light } from "@/core/config/VuetifyThemes";
import defaults from "@/core/config/VuetifyDefaults";
import { contrast } from "./contrast.test";

describe("W71 dialog chrome", () => {
  for (const [name, theme] of [
    ["light", light],
    ["dark", dark],
  ] as const) {
    it(`${name} card-header token exists with readable on-card-header`, () => {
      const colors = theme.colors as Record<string, string>;
      expect(colors["card-header"]).toBeTruthy();
      expect(colors["on-card-header"]).toBeTruthy();
      expect(contrast(colors["on-card-header"], colors["card-header"])).toBeGreaterThanOrEqual(
        4.5,
      );
    });
  }

  it("AppDialogClose nested defaults keep the X compact/text", () => {
    expect(defaults.AppDialogClose.VBtn.variant).toBe("text");
    expect(defaults.AppDialogClose.VBtn.size).toBe("small");
  });

  it("light card-header is the Settings grey plate", () => {
    const colors = light.colors as Record<string, string>;
    expect(colors["card-header"].toLowerCase()).toBe("#e8eaf6");
  });
});
