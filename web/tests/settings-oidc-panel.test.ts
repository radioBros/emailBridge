import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import { createPinia, setActivePinia } from "pinia";
import { OIDC_PRESET_KEY } from "@/features/settings/helpers/oidcPreset";
import en from "@/features/settings/locales/en.json";

vi.mock("@/api/oidc", () => ({
  listOidcProviders: vi.fn(async () => []),
  createOidcProvider: vi.fn(),
  patchOidcProvider: vi.fn(),
  deleteOidcProvider: vi.fn(),
}));

vi.mock("@/api/integrations", () => ({
  listIntegrations: vi.fn(async () => ({
    data: [
      {
        id: "sso-authentik",
        name: "Authentik",
        category: "sso",
        icon: "mdi-shield-key",
        configSchema: {
          [OIDC_PRESET_KEY]: {
            issuerUrlPattern: "https://<authentik-host>/application/o/<slug>/",
            scopes: ["openid", "email", "profile"],
            groupClaim: "groups",
            buttonLabel: "Authentik",
            helpText: "Register {origin}/v1/auth/oidc/<provider-id>/callback",
            redirectURIPath: "/v1/auth/oidc/<provider-id>/callback",
          },
        },
        secretFields: [],
        capabilities: [],
        installedCount: 0,
      },
    ],
  })),
}));

vi.mock("@/composables/useConfirm", () => ({
  useConfirm: () => async () => true,
}));

import OidcProvidersPanel from "@/features/settings/components/OidcProvidersPanel.vue";

const i18n = createI18n({
  legacy: false,
  locale: "en",
  messages: { en },
});

describe("OidcProvidersPanel preset prefill", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
  });

  it("prefills issuer scopes and group claim from a catalogue preset", async () => {
    const w = mount(OidcProvidersPanel, {
      global: {
        plugins: [i18n],
        stubs: {
          VDialog: {
            props: ["modelValue"],
            template: '<div v-if="modelValue" data-testid="oidc-dialog"><slot /></div>',
          },
          VForm: { template: "<form><slot /></form>" },
          VTextField: {
            props: ["modelValue"],
            emits: ["update:modelValue"],
            template:
              '<input :data-testid="$attrs[\'data-testid\']" :value="modelValue" @input="$emit(\'update:modelValue\', $event.target.value)" />',
          },
          VAlert: { template: '<div data-testid="oidc-preset-help"><slot /></div>' },
          VCard: { template: "<div><slot /><slot name=\"default\" /></div>" },
          VCardTitle: { template: "<div><slot /></div>" },
          VCardText: { template: "<div><slot /></div>" },
          VCardActions: { template: "<div><slot /></div>" },
          VBtn: { template: '<button type="button" v-bind="$attrs"><slot /></button>' },
          VSwitch: { template: "<div />" },
          VAvatar: { template: "<div />" },
          VIcon: { template: "<div />" },
          VChip: { template: "<span><slot /></span>" },
          VSpacer: { template: "<div />" },
          AutocompleteSearch: {
            props: ["modelValue", "items"],
            emits: ["update:modelValue"],
            template:
              '<button data-testid="oidc-preset" type="button" @click="$emit(\'update:modelValue\', \'sso-authentik\')">preset</button>',
          },
          AppCard: {
            template: '<div><slot name="header" /><slot /></div>',
          },
          AppSectionLoader: true,
          LoadingSkeleton: true,
        },
      },
    });
    await flushPromises();
    await w.get('[data-testid="oidc-add"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="oidc-dialog"]').exists()).toBe(true);
    await w.get('[data-testid="oidc-preset"]').trigger("click");
    await flushPromises();
    expect((w.get('[data-testid="oidc-issuer"]').element as HTMLInputElement).value).toBe(
      "https://<authentik-host>/application/o/<slug>/",
    );
    expect((w.get('[data-testid="oidc-scopes"]').element as HTMLInputElement).value).toBe(
      "openid, email, profile",
    );
    expect((w.get('[data-testid="oidc-group-claim"]').element as HTMLInputElement).value).toBe(
      "groups",
    );
    expect(w.get('[data-testid="oidc-preset-help"]').text()).toContain(
      "/v1/auth/oidc/<provider-id>/callback",
    );
  });
});
