import { watch } from "vue";
import { useTheme } from "vuetify";
import { dark as defaultDark, light as defaultLight } from "@/core/config/VuetifyThemes";
import { getConnectBranding, type BrandingPalette } from "@/api/connect";
import { useSessionStore } from "@/stores/session";

const cacheKey = (orgId: string): string => `eb.brand.palette.${orgId}`;

const cloneColors = (src: Record<string, string>): Record<string, string> => ({ ...src });

const readCache = (orgId: string): BrandingPalette | null => {
  try {
    const raw = localStorage.getItem(cacheKey(orgId));
    if (!raw) return null;
    return JSON.parse(raw) as BrandingPalette;
  } catch {
    return null;
  }
};

const writeCache = (orgId: string, palette: BrandingPalette | null): void => {
  try {
    if (!palette) {
      localStorage.removeItem(cacheKey(orgId));
      return;
    }
    localStorage.setItem(cacheKey(orgId), JSON.stringify(palette));
  } catch {
    /* private mode */
  }
};

const applyMode = (
  themeColors: Record<string, string>,
  defaults: Record<string, string>,
  overrides?: Record<string, string> | null,
): void => {
  const next = cloneColors(defaults);
  if (overrides) {
    for (const [k, v] of Object.entries(overrides)) {
      if (v) next[k] = v;
    }
  }
  for (const [k, v] of Object.entries(next)) {
    themeColors[k] = v;
  }
};

/** Apply org branding palette to Vuetify themes (W68). */
export const useBrandTheme = () => {
  const theme = useTheme();
  const session = useSessionStore();

  const applyPalette = (palette: BrandingPalette | null | undefined): void => {
    const lightDefaults = (defaultLight.colors ?? {}) as Record<string, string>;
    const darkDefaults = (defaultDark.colors ?? {}) as Record<string, string>;
    const lightTheme = theme.themes.value.light;
    const darkTheme = theme.themes.value.dark;
    if (lightTheme?.colors) {
      applyMode(lightTheme.colors as Record<string, string>, lightDefaults, palette?.light);
    }
    if (darkTheme?.colors) {
      applyMode(darkTheme.colors as Record<string, string>, darkDefaults, palette?.dark);
    }
  };

  const loadForOrg = async (orgId: string | undefined): Promise<void> => {
    if (!orgId) {
      applyPalette(null);
      return;
    }
    const cached = readCache(orgId);
    if (cached) applyPalette(cached);
    try {
      const { data } = await getConnectBranding();
      const palette = data.palette ?? null;
      if (data.primaryColor && (!palette?.light?.primary)) {
        const merged: BrandingPalette = {
          light: { ...(palette?.light ?? {}), primary: data.primaryColor },
          dark: palette?.dark,
        };
        writeCache(orgId, merged);
        applyPalette(merged);
        return;
      }
      writeCache(orgId, palette);
      applyPalette(palette);
    } catch {
      /* keep cache / defaults */
    }
  };

  const orgIdOf = (): string | undefined =>
    session.organizationId ?? session.me?.activeOrganizationId ?? undefined;

  const boot = async (): Promise<void> => {
    await loadForOrg(orgIdOf());
  };

  watch(
    () => orgIdOf(),
    (orgId) => {
      void loadForOrg(orgId);
    },
  );

  return { boot, applyPalette, loadForOrg };
};

/** Apply palette on the public connect page (no session). */
export const applyConnectPalette = (
  theme: ReturnType<typeof useTheme>,
  palette: BrandingPalette | null | undefined,
  primaryColor?: string,
): void => {
  const lightDefaults = (defaultLight.colors ?? {}) as Record<string, string>;
  const darkDefaults = (defaultDark.colors ?? {}) as Record<string, string>;
  const merged: BrandingPalette = {
    light: { ...(palette?.light ?? {}) },
    dark: { ...(palette?.dark ?? {}) },
  };
  if (primaryColor && !merged.light?.primary) {
    merged.light = { ...merged.light, primary: primaryColor };
  }
  const lightTheme = theme.themes.value.light;
  const darkTheme = theme.themes.value.dark;
  if (lightTheme?.colors) {
    applyMode(lightTheme.colors as Record<string, string>, lightDefaults, merged.light);
  }
  if (darkTheme?.colors) {
    applyMode(darkTheme.colors as Record<string, string>, darkDefaults, merged.dark);
  }
};
