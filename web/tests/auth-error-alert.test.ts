import { describe, expect, it } from "vitest";
import { mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import { createVuetify } from "vuetify";
import AuthErrorAlert from "@/features/auth/components/AuthErrorAlert.vue";
import type { LoginErrorState } from "@/features/auth/composables/useLoginFlow";
import { aliases, mdiSet } from "@/core/config/MdiIconSet";
import { dark, light } from "@/core/config/VuetifyThemes";
import defaults, { componentAliases } from "@/core/config/VuetifyDefaults";

const i18n = createI18n({
  legacy: false,
  locale: "en",
  messages: {
    en: {
      auth: {
        error_invalid_credentials: "Email or password is incorrect.",
        error_mfa_invalid: "That code is not valid. Try again.",
        error_account_locked: "Too many failed attempts. Try again in {sec}s.",
        error_account_locked_generic: "Too many failed attempts. Try again later.",
        error_common_password: "That password is too common. Choose a different one.",
        error_generic: "Something went wrong. Try again.",
      },
    },
  },
});

const vuetify = createVuetify({
  theme: { defaultTheme: "light", themes: { light, dark } },
  defaults,
  aliases: componentAliases,
  icons: { defaultSet: "mdi", aliases, sets: { mdi: mdiSet } },
});

const mountAlert = (error: LoginErrorState | null) =>
  mount(AuthErrorAlert, {
    props: { error },
    global: { plugins: [vuetify, i18n] },
  });

describe("AuthErrorAlert", () => {
  it("renders nothing without an error", () => {
    const w = mountAlert(null);
    expect(w.find('[role="alert"]').exists()).toBe(false);
  });

  it("renders credential and MFA errors", () => {
    expect(
      mountAlert({ code: "invalid_credentials", retryAfterSec: null }).text(),
    ).toContain("Email or password is incorrect.");
    expect(mountAlert({ code: "mfa_invalid", retryAfterSec: null }).text()).toContain(
      "That code is not valid",
    );
  });

  it("renders lockout with retry seconds", () => {
    expect(mountAlert({ code: "account_locked", retryAfterSec: 30 }).text()).toContain("30s");
  });

  it("renders common_password reason", () => {
    expect(
      mountAlert({
        code: "validation_failed",
        reason: "common_password",
        retryAfterSec: null,
      }).text(),
    ).toContain("too common");
  });
});
