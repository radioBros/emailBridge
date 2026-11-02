import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import settingsEn from "@/features/settings/locales/en.json";
import switchesEn from "@/lang/en/switches.json";
import aiEn from "@/features/ai/locales/en.json";

vi.mock("@/api/settings", () => ({
  getSettings: vi.fn(async () => ({
    body: {
      version: 3,
      values: {
        webhookTimeout: { value: "30s", source: "default", scope: "organization" },
        authSessionTtl: { value: "12h", source: "env", scope: "organization" },
        syncMaxIdle: { value: "29m", source: "global", scope: "organization" },
      },
      switches: {},
    },
    etag: "v3",
  })),
  patchSettings: vi.fn(async () => ({
    body: { version: 4, values: {}, switches: {} },
    etag: "v4",
  })),
  listFeatureSwitches: vi.fn(async () => ({
    data: [
      {
        name: "sandbox.enabled",
        description: "Sandbox",
        scopes: ["global", "organization"],
        default: false,
        phase: "P1",
        effective: false,
        source: "default",
      },
      {
        name: "webhook.mtls",
        description: "mTLS",
        scopes: ["global", "organization"],
        default: false,
        phase: "P3",
        effective: false,
        source: "organization",
      },
    ],
  })),
}));

vi.mock("@/api/auth", () => ({
  auth: {
    useSession: () => ({ value: { data: null, isPending: false } }),
    signout: vi.fn(),
  },
}));

vi.mock("@/api/me", () => ({
  getMe: vi.fn(),
  patchMe: vi.fn(),
  getMySettings: vi.fn(),
  patchUiSettings: vi.fn(),
}));

vi.mock("@/features/settings/components/OidcProvidersPanel.vue", () => ({
  default: { name: "OidcProvidersPanel", template: '<div data-testid="oidc-panel" />' },
}));

vi.mock("@/features/ai/components/AiProviderPanel.vue", () => ({
  default: { name: "AiProviderPanel", template: '<div data-testid="ai-panel" />' },
}));

vi.mock("@/features/ai/components/McpServerPanel.vue", () => ({
  default: { name: "McpServerPanel", template: '<div data-testid="mcp-panel" />' },
}));

import SettingsPage from "@/features/settings/pages/SettingsPage.vue";
import { patchSettings } from "@/api/settings";
import { useSessionStore } from "@/stores/session";

const i18n = createI18n({
  legacy: false,
  locale: "en",
  messages: { en: { ...settingsEn, ...switchesEn, ai: aiEn.ai } },
});

const seedOwner = (): void => {
  const session = useSessionStore();
  session.me = {
    kind: "user",
    mfa: true,
    permissions: [],
    memberships: [],
    role: "owner",
    user: {
      id: "u1",
      email: "a@b.c",
      name: "Ada",
      locale: "en",
      instanceRole: "owner",
      status: "active",
    },
  } as never;
};

const mountPage = async (): Promise<VueWrapper> => {
  const w = mount(SettingsPage, {
    attachTo: document.body,
    global: { plugins: [i18n] },
  });
  await flushPromises();
  return w;
};

const search = async (w: VueWrapper, q: string): Promise<void> => {
  await w.get('[data-testid="settings-search"] input').setValue(q);
  await flushPromises();
};

const resultIds = (w: VueWrapper): string[] =>
  w.findAll('[data-testid="settings-search-result"]').map((r) => r.attributes("data-result") ?? "");

const saveButton = (w: VueWrapper) => {
  const btn = w.findAll("button").find((b) => b.text() === "Save");
  if (!btn) throw new Error("save button missing");
  return btn;
};

const headerTitle = (w: VueWrapper): string => w.get(".app-card-header .text-title-medium").text();

describe("Settings quick search", () => {
  beforeEach(() => {
    vi.mocked(patchSettings).mockClear();
    seedOwner();
    document.body.innerHTML = "";
  });

  it("replaces the tabs with results from several tabs and highlights the match", async () => {
    const w = await mountPage();
    expect(w.find(".v-tabs").exists()).toBe(true);

    await search(w, "webhook");
    expect(w.find(".v-tabs").exists()).toBe(false);
    const ids = resultIds(w);
    expect(ids).toContain("field:webhookTimeout");
    expect(ids).toContain("switch:webhook.mtls");
    // Label matches come before key-only matches.
    expect(ids.indexOf("field:webhookTimeout")).toBeLessThan(ids.indexOf("switch:webhook.mtls"));

    const first = w.get('[data-result="field:webhookTimeout"]');
    expect(first.text()).toContain("Webhooks & egress");
    expect(first.text()).toContain("default");
    expect(first.find("mark").text()).toBe("Webhook");
    expect(w.get('[data-testid="settings-search-count"]').text()).toContain(String(ids.length));
    w.unmount();
  });

  it("matches accents-insensitively and on several words in any order", async () => {
    const w = await mountPage();
    await search(w, "TIMÉOUT webhook");
    expect(resultIds(w)).toEqual(["field:webhookTimeout"]);
    w.unmount();
  });

  it("editing a field result marks the draft and Save sends it", async () => {
    const w = await mountPage();
    await search(w, "webhook timeout");
    await w.get('[data-result="field:webhookTimeout"] input').setValue("45s");
    await flushPromises();
    expect(w.text()).toContain("Unsaved changes");
    await saveButton(w).trigger("click");
    await flushPromises();
    expect(patchSettings).toHaveBeenCalledWith("organization", { webhookTimeout: "45s" }, "v3");
    w.unmount();
  });

  it("a switch edited from the results is saved although a field tab is selected", async () => {
    const w = await mountPage();
    await search(w, "mtls");
    const input = w.get('[data-result="switch:webhook.mtls"] input[type="checkbox"]');
    await input.setValue(true);
    await flushPromises();
    expect(w.text()).toContain("Unsaved changes");
    await saveButton(w).trigger("click");
    await flushPromises();
    expect(patchSettings).toHaveBeenCalledWith("organization", { "webhook.mtls": true }, "v3");
    w.unmount();
  });

  it("shows validation errors on the result and blocks Save", async () => {
    const w = await mountPage();
    await search(w, "webhook timeout");
    await w.get('[data-result="field:webhookTimeout"] input').setValue("soon");
    await flushPromises();
    expect(w.get('[data-result="field:webhookTimeout"]').text()).toContain(
      "Use a duration like 30s, 5m, 24h or 7d",
    );
    expect(saveButton(w).attributes("disabled")).toBeDefined();
    w.unmount();
  });

  it("Open on a panel result clears the search and switches to its tab", async () => {
    const w = await mountPage();
    await search(w, "sso providers");
    expect(resultIds(w)).toEqual(["section:oidc"]);
    await w.get('[data-testid="settings-search-open"]').trigger("click");
    await flushPromises();
    expect((w.get('[data-testid="settings-search"] input').element as HTMLInputElement).value).toBe("");
    expect(w.find(".v-tabs").exists()).toBe(true);
    expect(headerTitle(w)).toBe("Security");
    w.unmount();
  });

  it("finds the AI provider and the MCP server as two panels, each opening its own", async () => {
    const w = await mountPage();
    await search(w, "mcp server");
    expect(resultIds(w)).toEqual(["section:mcp"]);
    await search(w, "ai provider");
    expect(resultIds(w)).toContain("section:ai");
    expect(resultIds(w)).not.toContain("section:mcp");
    const scrolled: string[] = [];
    const proto = HTMLElement.prototype as unknown as { scrollIntoView?: () => void };
    const original = proto.scrollIntoView;
    proto.scrollIntoView = function (this: HTMLElement) {
      scrolled.push(this.id);
    };
    const open = w
      .findAll('[data-testid="settings-search-result"]')
      .find((r) => r.attributes("data-result") === "section:ai")
      ?.find('[data-testid="settings-search-open"]');
    await open?.trigger("click");
    await flushPromises();
    expect(scrolled).toEqual(["settings-section-ai"]);
    expect(headerTitle(w)).toBe("Feature switches");
    expect(w.find("#settings-section-mcp [data-testid=\"mcp-panel\"]").exists()).toBe(true);
    await search(w, "mcp server");
    await w.get('[data-testid="settings-search-open"]').trigger("click");
    await flushPromises();
    expect(scrolled).toEqual(["settings-section-ai", "settings-section-mcp"]);
    proto.scrollIntoView = original;
    w.unmount();
  });

  it("clearing the search restores the selected tab and keeps unsaved drafts", async () => {
    const w = await mountPage();
    const syncTab = w.findAll(".v-tab").find((t) => t.text() === "Sync defaults");
    await syncTab?.trigger("click");
    await flushPromises();
    expect(headerTitle(w)).toBe("Sync defaults");

    await search(w, "max idle");
    expect(headerTitle(w)).toBe("Search results");
    await w.get('[data-result="field:syncMaxIdle"] input').setValue("31m");
    await flushPromises();

    await search(w, "");
    expect(w.find(".v-tabs").exists()).toBe(true);
    expect(headerTitle(w)).toBe("Sync defaults");
    expect(w.text()).toContain("Unsaved changes");
    w.unmount();
  });

  it("a switch edited in search stays unsaved after clearing to a settings tab, and Save sends it", async () => {
    const w = await mountPage();
    await search(w, "mtls");
    await w.get('[data-result="switch:webhook.mtls"] input[type="checkbox"]').setValue(true);
    await flushPromises();

    await search(w, "");
    expect(headerTitle(w)).toBe("General");
    expect(w.text()).toContain("Unsaved changes");
    expect(saveButton(w).attributes("disabled")).toBeUndefined();
    await saveButton(w).trigger("click");
    await flushPromises();
    expect(patchSettings).toHaveBeenCalledWith("organization", { "webhook.mtls": true }, "v3");
    w.unmount();
  });

  it("Save sends drafts from every tab and the switches in one patch", async () => {
    const w = await mountPage();
    await search(w, "webhook timeout");
    await w.get('[data-result="field:webhookTimeout"] input').setValue("45s");
    await search(w, "mtls");
    await w.get('[data-result="switch:webhook.mtls"] input[type="checkbox"]').setValue(true);
    await search(w, "max idle");
    await w.get('[data-result="field:syncMaxIdle"] input').setValue("31m");
    await search(w, "");

    await saveButton(w).trigger("click");
    await flushPromises();
    expect(patchSettings).toHaveBeenCalledTimes(1);
    expect(patchSettings).toHaveBeenCalledWith(
      "organization",
      { "webhook.mtls": true, webhookTimeout: "45s", syncMaxIdle: "31m" },
      "v3",
    );
    expect(w.text()).not.toContain("Unsaved changes");
    w.unmount();
  });

  it("an invalid draft on a tab that is not on screen keeps Save disabled", async () => {
    const w = await mountPage();
    await search(w, "webhook timeout");
    await w.get('[data-result="field:webhookTimeout"] input').setValue("soon");
    await search(w, "");
    expect(headerTitle(w)).toBe("General");
    expect(w.text()).toContain("Unsaved changes");
    expect(saveButton(w).attributes("disabled")).toBeDefined();
    w.unmount();
  });

  it("highlights a field found only through its hint", async () => {
    const w = await mountPage();
    await search(w, "unlimited");
    expect(resultIds(w)).toContain("field:quotaAccounts");
    const marks = w.findAll('[data-result="field:quotaAccounts"] mark');
    expect(marks.map((m) => m.text())).toEqual(["unlimited"]);
    w.unmount();
  });

  it("shows an empty state with the query", async () => {
    const w = await mountPage();
    await search(w, "zzqqxx");
    const empty = w.get('[data-testid="settings-search-empty"]');
    expect(empty.text()).toContain("No setting matches “zzqqxx”");
    expect(resultIds(w)).toEqual([]);
    w.unmount();
  });

  it("the / key focuses the search, but not while typing in another input", async () => {
    const w = await mountPage();
    const input = w.get('[data-testid="settings-search"] input').element as HTMLInputElement;

    const other = w.get('[data-testid="settings-search"]').element.ownerDocument.createElement("input");
    document.body.appendChild(other);
    other.focus();
    other.dispatchEvent(new KeyboardEvent("keydown", { key: "/", bubbles: true, cancelable: true }));
    expect(document.activeElement).toBe(other);

    other.blur();
    const ev = new KeyboardEvent("keydown", { key: "/", bubbles: true, cancelable: true });
    document.body.dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
    expect(document.activeElement).toBe(input);
    w.unmount();
  });
});
