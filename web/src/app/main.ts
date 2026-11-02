// FIRST import: Vuetify's base stylesheet declares the cascade-layer order. In `vite dev` every
// component's CSS is injected as it is imported, so if App.vue's components came first they would
// fix the layer order themselves and the theme's colour classes (`text-primary`, …) would lose to
// the components' own text colour — grey buttons in dev only, correct in the build.
// @ts-expect-error Vuetify ships CSS without type declarations
import "vuetify/styles";
// Self-hosted IBM Plex (latin + latin-ext + cyrillic). Weights match the former Google Fonts link.
// font-display: swap is set by @fontsource. zh/ja keep the system fallback stack in --font-sans.
import "@fontsource/ibm-plex-sans/latin-400.css";
import "@fontsource/ibm-plex-sans/latin-500.css";
import "@fontsource/ibm-plex-sans/latin-600.css";
import "@fontsource/ibm-plex-sans/latin-700.css";
import "@fontsource/ibm-plex-sans/latin-ext-400.css";
import "@fontsource/ibm-plex-sans/latin-ext-500.css";
import "@fontsource/ibm-plex-sans/latin-ext-600.css";
import "@fontsource/ibm-plex-sans/latin-ext-700.css";
import "@fontsource/ibm-plex-sans/cyrillic-400.css";
import "@fontsource/ibm-plex-sans/cyrillic-500.css";
import "@fontsource/ibm-plex-sans/cyrillic-600.css";
import "@fontsource/ibm-plex-sans/cyrillic-700.css";
import "@fontsource/ibm-plex-mono/latin-500.css";
import "@fontsource/ibm-plex-mono/latin-ext-500.css";
import "@fontsource/ibm-plex-mono/cyrillic-500.css";
import { createApp } from "vue";
import App from "@/app/App.vue";
import router from "@/app/router";
import i18n, { bootI18n } from "@/app/i18n";
import pinia from "@/app/plugins/pinia";
import tippy from "@/app/plugins/tippy";
import vuetify from "@/app/plugins/vuetify";
import { installErrorHandlers } from "@/core/errors/installErrorHandlers";
import "@/styles/main.scss";

const start = async (): Promise<void> => {
  await bootI18n();

  const app = createApp(App);
  app.use(pinia);
  installErrorHandlers(app);
  app.use(router);
  app.use(i18n);
  app.use(vuetify);
  app.use(tippy);
  app.mount("#app");
};

void start();
