import { config } from "@vue/test-utils";
import { createVuetify } from "vuetify";
import { aliases, mdiSet } from "@/core/config/MdiIconSet";
import { dark, light } from "@/core/config/VuetifyThemes";
import defaults, { componentAliases } from "@/core/config/VuetifyDefaults";
import { createI18n } from "vue-i18n";
import { createPinia, setActivePinia } from "pinia";

// Avoid pulling tippy.css / full component CSS trees into every suite.
vi.mock("vue-tippy", () => ({
  default: { install: () => undefined },
  directive: {},
}));

// happy-dom lacks visualViewport; Vuetify overlays (dialogs, drawers) read it.
if (typeof window !== "undefined" && !window.visualViewport) {
  Object.defineProperty(window, "visualViewport", {
    configurable: true,
    value: {
      width: window.innerWidth || 1024,
      height: window.innerHeight || 768,
      offsetLeft: 0,
      offsetTop: 0,
      pageLeft: 0,
      pageTop: 0,
      scale: 1,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false,
    },
  });
}

const vuetify = createVuetify({
  theme: { defaultTheme: "light", themes: { light, dark } },
  defaults,
  aliases: componentAliases,
  icons: { defaultSet: "mdi", aliases, sets: { mdi: mdiSet } },
});

const i18n = createI18n({
  legacy: false,
  locale: "en",
  messages: {
    en: {
      tables: {
        search_placeholder: "Search",
        no_results: "No results",
        configure_columns: "Columns",
        reset_widths: "Reset widths",
        resize_column: "Resize {column}",
        show: "Show",
        entries: "rows",
        items_per_page: "Rows per page",
        showing_items: "{from}-{to} of {total}",
        sort_by: "Sort by",
        sort_ascending: "Asc",
        sort_descending: "Desc",
        load_more_failed: "Failed",
        retry: "Retry",
      },
      common: {
        search: "Search",
        close: "Close",
        states: { loading: "Loading", refreshing: "Refreshing" },
      },
      confirm: { confirm: "Confirm", cancel: "Cancel" },
      update: {
        available_title: "Update",
        available_body: "Reload",
        reload: "Reload",
        later: "Later",
        indicator_tooltip: "Update",
      },
      shell: { copy_trace: "Copy trace" },
      problems: { internal_error: "Internal error", unauthenticated: "Unauthenticated" },
    },
  },
});

config.global.plugins = [vuetify, i18n];
config.global.stubs = {
  teleport: true,
};

beforeEach(() => {
  setActivePinia(createPinia());
});
