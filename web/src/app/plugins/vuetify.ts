// @ts-expect-error Vuetify ships CSS without type declarations
import "vuetify/styles";

import { createVuetify } from "vuetify";
import { de, en, es, fr, it, ja, pt, ru, zhHans } from "vuetify/locale";

import defaults, { componentAliases } from "@/core/config/VuetifyDefaults";
import { aliases, mdiSet } from "@/core/config/MdiIconSet";
import { dark, light } from "@/core/config/VuetifyThemes";
import { DEFAULT_LOCALE, vuetifyOf } from "@/app/locales";

export default createVuetify({
  theme: {
    // system follows prefers-color-scheme; user preference (boot) overrides.
    defaultTheme: "system",
    themes: { light, dark },
  },
  defaults,
  // W67 role aliases (<v-btn-inline>, <v-avatar-hero> …) — sized by their keys in VuetifyDefaults.
  aliases: componentAliases,
  display: {
    mobileBreakpoint: "sm",
    thresholds: {
      xs: 0,
      sm: 600,
      md: 768,
      lg: 1025,
      xl: 1280,
      xxl: 1800,
    },
  },
  icons: {
    defaultSet: "mdi",
    aliases,
    sets: { mdi: mdiSet },
  },
  locale: {
    locale: vuetifyOf(DEFAULT_LOCALE),
    fallback: "en",
    messages: { en, it, es, fr, de, pt, ru, zhHans, ja },
  },
});
