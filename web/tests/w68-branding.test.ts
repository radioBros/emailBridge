import { describe, expect, it } from "vitest";
import { contrast, isHexColor } from "@/utils/contrast";
import { dark, light } from "@/core/config/VuetifyThemes";

describe("w68 contrast helpers", () => {
  it("accepts hex colours", () => {
    expect(isHexColor("#166534")).toBe(true);
    expect(isHexColor("#fff")).toBe(false);
    expect(isHexColor("166534")).toBe(false);
  });

  it("matches theme AA pairs used by the branding editor", () => {
    const colors = light.colors as Record<string, string>;
    expect(contrast(colors["on-surface"], colors.surface)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(colors["on-primary"], colors.primary)).toBeGreaterThanOrEqual(4.5);
    const d = dark.colors as Record<string, string>;
    expect(contrast(d["on-surface"], d.surface)).toBeGreaterThanOrEqual(4.5);
  });
});
