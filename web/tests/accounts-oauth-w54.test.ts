import { describe, expect, it, vi, beforeEach } from "vitest";
import { mount, flushPromises } from "@vue/test-utils";
import { createPinia, setActivePinia } from "pinia";
import { createI18n } from "vue-i18n";
import { createVuetify } from "vuetify";
import * as components from "vuetify/components";
import * as directives from "vuetify/directives";
import en from "@/features/accounts/locales/en.json";
import AccountOAuthPanel from "@/features/accounts/components/AccountOAuthPanel.vue";
import * as api from "@/api/accountsOauth";

vi.mock("@/api/accountsOauth", () => ({
  getAccountOauth: vi.fn(),
  listAccountServerSignatures: vi.fn(),
  startAccountOauth: vi.fn(),
  disconnectAccountOauth: vi.fn(),
  exportAccountOauthToken: vi.fn(),
}));

const vuetify = createVuetify({ components, directives });
const i18n = createI18n({
  legacy: false,
  locale: "en",
  messages: { en },
});

describe("AccountOAuthPanel W54", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.mocked(api.getAccountOauth).mockResolvedValue({
      connected: true,
      appId: "oap_1",
      expiresAt: "2026-10-27T12:00:00Z",
      scopes: ["mail"],
    });
    vi.mocked(api.listAccountServerSignatures).mockResolvedValue({
      data: [{ email: "a@ex.com", displayName: "A", isDefault: true, signature: "<b>x</b>" }],
    });
  });

  it("shows connected oauth and signatures", async () => {
    const w = mount(AccountOAuthPanel, {
      props: { accountId: "acc_1", provider: "gmail", tokenExportEnabled: true, canExportToken: true },
      global: { plugins: [vuetify, i18n, createPinia()] },
    });
    await flushPromises();
    expect(w.text()).toContain("oap_1");
    expect(w.text()).toContain("a@ex.com");
    expect(w.text()).toContain("Export access token");
  });

  it("shows connect form when disconnected", async () => {
    vi.mocked(api.getAccountOauth).mockResolvedValue({ connected: false });
    vi.mocked(api.listAccountServerSignatures).mockRejectedValue(new Error("nope"));
    const w = mount(AccountOAuthPanel, {
      props: { accountId: "acc_2" },
      global: { plugins: [vuetify, i18n, createPinia()] },
    });
    await flushPromises();
    expect(w.text()).toContain("not connected");
    expect(w.text()).toContain("Connect");
  });
});
