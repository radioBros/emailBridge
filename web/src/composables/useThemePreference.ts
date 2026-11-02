import { onMounted, onUnmounted, ref, watch } from "vue";
import { useTheme } from "vuetify";
import { getMySettings, patchUiSettings } from "@/api/me";

export type ThemeChoice = "light" | "dark" | "system";

const STORAGE_KEY = "eb.ui.theme";

/** Module singleton — one preference for the whole SPA. */
const choice = ref<ThemeChoice>(readStored());
let booted = false;
let themeApi: ReturnType<typeof useTheme> | null = null;

const isChoice = (v: unknown): v is ThemeChoice =>
  v === "light" || v === "dark" || v === "system";

function readStored(): ThemeChoice {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (isChoice(raw)) return raw;
  } catch {
    /* private mode */
  }
  return "system";
}

function writeStored(next: ThemeChoice): void {
  try {
    localStorage.setItem(STORAGE_KEY, next);
  } catch {
    /* ignore */
  }
}

/** Apply via Vuetify 4 (`system` follows prefers-color-scheme live). */
function applyToVuetify(c: ThemeChoice): void {
  if (!themeApi) return;
  themeApi.change(c);
}

/**
 * Single source of truth for light / dark / system.
 * Call `boot()` once from the admin shell; profile + shell toggle share this.
 * Nothing else may overwrite the preference after boot except setChoice.
 */
export const useThemePreference = () => {
  const theme = useTheme();
  themeApi = theme;

  const setChoice = async (
    next: ThemeChoice,
    opts: { persist?: boolean } = {},
  ): Promise<void> => {
    choice.value = next;
    writeStored(next);
    applyToVuetify(next);
    if (opts.persist === false) return;
    try {
      await patchUiSettings({ theme: next });
    } catch {
      /* offline / first paint — local + Vuetify still correct */
    }
  };

  const toggleLightDark = (): void => {
    const dark = theme.global.current.value.dark;
    // Auth pages have no session — skip the settings PATCH (persist false).
    void setChoice(dark ? "light" : "dark", { persist: booted });
  };

  /** Load server preference once; never re-apply a default that fights the user. */
  const boot = async (): Promise<void> => {
    if (booted) {
      applyToVuetify(choice.value);
      return;
    }
    booted = true;
    applyToVuetify(choice.value);
    try {
      const settings = await getMySettings();
      const ui = settings.settings?.ui ?? {};
      if (isChoice(ui.theme)) {
        choice.value = ui.theme;
        writeStored(ui.theme);
        applyToVuetify(ui.theme);
      }
    } catch {
      /* keep stored / system */
    }
  };

  /**
   * Auth / connect shell: apply the stored choice (system / light / dark).
   * OS changes only re-apply while choice stays `system` — a sun/moon toggle must stick.
   */
  const followSystemWhileMounted = (): void => {
    onMounted(() => {
      applyToVuetify(choice.value);
      const localMq = window.matchMedia("(prefers-color-scheme: dark)");
      const onChange = (): void => {
        if (!booted && choice.value === "system") applyToVuetify("system");
      };
      localMq.addEventListener("change", onChange);
      onUnmounted(() => localMq.removeEventListener("change", onChange));
    });
  };

  watch(choice, (c) => {
    applyToVuetify(c);
  });

  return {
    choice,
    setChoice,
    toggleLightDark,
    boot,
    followSystemWhileMounted,
  };
};
