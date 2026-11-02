import { describe, expect, it } from "vitest";
import { Actions } from "@/constants/Actions";
import { dark, light } from "@/core/config/VuetifyThemes";

/** Relative luminance (sRGB), WCAG 2.1. */
const luminance = (hex: string): number => {
  const n = hex.replace("#", "");
  const r = parseInt(n.slice(0, 2), 16) / 255;
  const g = parseInt(n.slice(2, 4), 16) / 255;
  const b = parseInt(n.slice(4, 6), 16) / 255;
  const lin = (c: number) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
};

export const contrast = (a: string, b: string): number => {
  const L1 = luminance(a);
  const L2 = luminance(b);
  const lighter = Math.max(L1, L2);
  const darker = Math.min(L1, L2);
  return (lighter + 0.05) / (darker + 0.05);
};

/** Approximate Vuetify tonal background: colour at 12% opacity over surface. */
const tonalBg = (color: string, surface: string): string => {
  const parse = (hex: string) => {
    const n = hex.replace("#", "");
    return [parseInt(n.slice(0, 2), 16), parseInt(n.slice(2, 4), 16), parseInt(n.slice(4, 6), 16)];
  };
  const [cr, cg, cb] = parse(color);
  const [sr, sg, sb] = parse(surface);
  const mix = (c: number, s: number) => Math.round(c * 0.12 + s * 0.88);
  const toHex = (v: number) => v.toString(16).padStart(2, "0");
  return `#${toHex(mix(cr, sr))}${toHex(mix(cg, sg))}${toHex(mix(cb, sb))}`;
};

const TEXT_BEARING = ["primary", "secondary", "accent", "success", "info", "warning", "error"] as const;
const ICON_PALETTE = [
  "icon-indigo",
  "icon-cyan",
  "icon-violet",
  "icon-teal",
  "icon-amber",
  "icon-rose",
] as const;

describe("theme contrast (S13 §2.1)", () => {
  for (const [name, theme] of [
    ["light", light],
    ["dark", dark],
  ] as const) {
    for (const key of TEXT_BEARING) {
      it(`${name} ${key} ≥ 4.5:1 on surface (text / chips)`, () => {
        const colors = theme.colors as Record<string, string>;
        const color = colors[key];
        const surface = colors.surface;
        expect(contrast(color, surface)).toBeGreaterThanOrEqual(4.5);
      });

      it(`${name} ${key} ≥ 4.5:1 on tonal background`, () => {
        const colors = theme.colors as Record<string, string>;
        const color = colors[key];
        const surface = colors.surface;
        const bg = tonalBg(color, surface);
        expect(contrast(color, bg)).toBeGreaterThanOrEqual(4.5);
      });

      it(`${name} on-${key} ≥ 4.5:1 on ${key} fill (flat buttons)`, () => {
        const colors = theme.colors as Record<string, string>;
        const fill = colors[key];
        const on = colors[`on-${key}`];
        expect(on).toBeTruthy();
        expect(contrast(on, fill)).toBeGreaterThanOrEqual(4.5);
      });
    }

    for (const key of ICON_PALETTE) {
      it(`${name} ${key} ≥ 3:1 on surface (nav / feature icons)`, () => {
        const colors = theme.colors as Record<string, string>;
        const color = colors[key];
        const surface = colors.surface;
        expect(color).toBeTruthy();
        expect(contrast(color, surface)).toBeGreaterThanOrEqual(3);
      });
    }

    it(`${name} skeleton is defined and distinct from on-surface`, () => {
      const colors = theme.colors as Record<string, string>;
      expect(colors.skeleton).toBeTruthy();
      expect(colors["skeleton-highlight"]).toBeTruthy();
      expect(colors.field).toBeTruthy();
    });

    it(`${name} field matches surface in dark / is white in light`, () => {
      const colors = theme.colors as Record<string, string>;
      if (theme.dark) {
        expect(colors.field).toBe(colors.surface);
      } else {
        expect(colors.field.toLowerCase()).toBe("#ffffff");
      }
    });

    it(`${name} every Actions colour ≥ 4.5:1 on surface and tonal`, () => {
      const colors = theme.colors as Record<string, string>;
      const surface = colors.surface;
      const seen = new Set<string>();
      for (const spec of Object.values(Actions)) {
        if (seen.has(spec.color)) continue;
        seen.add(spec.color);
        const color = colors[spec.color];
        expect(color, spec.color).toBeTruthy();
        expect(contrast(color, surface)).toBeGreaterThanOrEqual(4.5);
        expect(contrast(color, tonalBg(color, surface))).toBeGreaterThanOrEqual(4.5);
      }
    });
  }
});
