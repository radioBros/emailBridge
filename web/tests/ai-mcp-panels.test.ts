import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount, type VueWrapper } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import aiEn from "@/features/ai/locales/en.json";

const { get, post, put, rawPatch } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
  rawPatch: vi.fn(),
}));

vi.mock("@/api/client", () => ({
  api: { get, post, put, raw: { patch: rawPatch } },
  ApiProblemError: class extends Error {
    problem: { status: number };
    constructor(p: { status: number }) {
      super("problem");
      this.problem = p;
    }
  },
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({ messageOf: (e: unknown) => (e instanceof Error ? e.message : String(e)) }),
}));

import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AiProviderPanel from "@/features/ai/components/AiProviderPanel.vue";
import McpServerPanel from "@/features/ai/components/McpServerPanel.vue";
import { dedicatedUrl, mainUrl, mcpClientConfig } from "@/features/ai/helpers/mcp";
import { modelItems } from "@/features/ai/helpers/providers";

const i18n = () =>
  createI18n({ legacy: false, locale: "en", missingWarn: false, fallbackWarn: false, messages: { en: { ...aiEn } } });

const settings = (over: Record<string, unknown> = {}) => ({
  provider: "openai",
  model: "gpt-4o",
  features: ["summary"],
  piiRedact: true,
  bodyMaxChars: 8000,
  apiKeySet: true,
  providerKeysSet: ["openai"],
  embeddings: { apiKeySet: false, configured: true, effectiveModel: "text-embedding-3-small" },
  ...over,
});

const byTestId = (w: VueWrapper, id: string) =>
  w.findAllComponents(AutocompleteSearch).find((c) => c.attributes("data-testid") === id);

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  put.mockReset();
  rawPatch.mockReset();
});

describe("AI provider panel", () => {
  it("connects with the stored key and fills the model picker", async () => {
    get.mockResolvedValue(settings());
    post.mockResolvedValue({ models: [{ id: "gpt-4o", name: "gpt-4o" }, { id: "gpt-4.1", name: "GPT-4.1" }] });
    const w = mount(AiProviderPanel, { global: { plugins: [i18n()] } });
    await flushPromises();
    await w.get('[data-testid="ai-connect"]').trigger("click");
    await flushPromises();
    expect(post).toHaveBeenCalledWith("/ai/models", { provider: "openai", baseUrl: undefined, apiKey: undefined });
    expect(w.get('[data-testid="ai-connected"]').text()).toContain("2 models");
    const items = byTestId(w, "ai-model")?.props("items") as Array<{ value: string }>;
    expect(items.map((i) => i.value)).toEqual(["gpt-4o", "gpt-4.1"]);
    w.unmount();
  });

  it("shows the provider's refusal and keeps the form", async () => {
    get.mockResolvedValue(settings());
    post.mockRejectedValue(new Error("The AI provider rejected the API key."));
    const w = mount(AiProviderPanel, { global: { plugins: [i18n()] } });
    await flushPromises();
    await w.get('[data-testid="ai-api-key"] input').setValue("sk-wrong");
    await w.get('[data-testid="ai-connect"]').trigger("click");
    await flushPromises();
    expect(post).toHaveBeenCalledWith("/ai/models", expect.objectContaining({ apiKey: "sk-wrong" }));
    expect(w.get('[data-testid="ai-connect-error"]').text()).toContain("rejected the API key");
    w.unmount();
  });

  it("switching provider clears the model; None saves without model or key", async () => {
    get.mockResolvedValue(settings());
    put.mockResolvedValue(settings({ provider: "none", model: "", apiKeySet: false }));
    const w = mount(AiProviderPanel, { global: { plugins: [i18n()] } });
    await flushPromises();
    byTestId(w, "ai-provider")?.vm.$emit("update:modelValue", "gemini");
    await flushPromises();
    expect(byTestId(w, "ai-model")?.props("modelValue")).toBe("");
    expect(w.get('[data-testid="ai-api-key"]').text()).toContain("Encrypted at rest");
    byTestId(w, "ai-provider")?.vm.$emit("update:modelValue", "none");
    await flushPromises();
    expect(w.find('[data-testid="ai-none-hint"]').exists()).toBe(true);
    expect(w.find('[data-testid="ai-connect"]').exists()).toBe(false);
    await w.get('[data-testid="ai-save"]').trigger("click");
    await flushPromises();
    expect(put).toHaveBeenCalledWith(
      "/ai/settings",
      expect.objectContaining({ provider: "none", model: undefined, baseUrl: undefined }),
    );
    expect(put.mock.calls[0][1]).not.toHaveProperty("apiKey");
    w.unmount();
  });

  it("loads embedding models from the main provider", async () => {
    get.mockResolvedValue(settings({ provider: "gemini", model: "gemini-2.5-flash", providerKeysSet: ["gemini"] }));
    post.mockResolvedValue({ models: [{ id: "gemini-embedding-001", name: "Gemini Embedding" }] });
    const w = mount(AiProviderPanel, { global: { plugins: [i18n()] } });
    await flushPromises();
    await w.get('[data-testid="ai-emb-connect"]').trigger("click");
    await flushPromises();
    expect(post).toHaveBeenCalledWith("/ai/models", expect.objectContaining({ provider: "gemini", kind: "embeddings" }));
    expect(byTestId(w, "ai-emb-model-pick")).toBeTruthy();
    w.unmount();
  });
});

const status = (over: Record<string, unknown> = {}) => ({
  listen: ":8090",
  listening: true,
  activeAddr: "[::]:8090",
  mainPath: "/v1/mcp",
  dedicatedPath: "/mcp",
  mainUrl: "https://mail.example.com/v1/mcp",
  canChange: true,
  toolsets: [{ id: "curated", tools: 11 }, { id: "messages", tools: 14 }],
  ...over,
});

const mockMcpGets = (st: Record<string, unknown>, enabled = false, locked = false): void => {
  get.mockImplementation(async (path: string) => {
    if (path === "/mcp/server") return st;
    if (path === "/feature-switches") return { data: [{ name: "mcp.enabled", effective: enabled, locked }] };
    throw new Error(`unexpected ${path}`);
  });
};

describe("MCP server panel", () => {
  it("shows both URLs, ready-to-paste configs and the toolsets", async () => {
    mockMcpGets(status());
    const w = mount(McpServerPanel, { global: { plugins: [i18n()], stubs: { RouterLink: true } } });
    await flushPromises();
    expect(w.get('[data-testid="mcp-main-url"]').text()).toBe("https://mail.example.com/v1/mcp");
    expect(w.get('[data-testid="mcp-dedicated-url"]').text()).toMatch(/^http:\/\/.+:8090\/mcp$/);
    expect(w.get('[data-testid="mcp-snippet"]').text()).toContain("claude mcp add --transport http emailbridge");
    expect(w.get('[data-testid="mcp-snippet"]').text()).toContain("Bearer <API key>");
    await w.get('[data-testid="mcp-client-vscode"]').trigger("click");
    await flushPromises();
    const cfg = JSON.parse(w.get('[data-testid="mcp-snippet"]').text());
    expect(cfg.servers.emailbridge.type).toBe("http");
    expect(w.get('[data-testid="mcp-toolset-messages"]').text()).toContain("14");
    expect(w.text()).toContain("Tools (25)");
    expect(w.find('[data-testid="mcp-api-keys-link"]').exists()).toBe(true);
    w.unmount();
  });

  it("toggles mcp.enabled and applies the port", async () => {
    mockMcpGets(status({ listen: "", listening: false, activeAddr: undefined }));
    rawPatch.mockResolvedValue({ data: { values: {}, switches: {}, version: 2 }, headers: {} });
    put.mockResolvedValue(status({ listen: ":8091", activeAddr: "[::]:8091" }));
    const w = mount(McpServerPanel, { global: { plugins: [i18n()], stubs: { RouterLink: true } } });
    await flushPromises();
    expect(w.find('[data-testid="mcp-dedicated-url"]').exists()).toBe(false);
    await w.get('[data-testid="mcp-enabled"] input').setValue(true);
    await flushPromises();
    expect(rawPatch).toHaveBeenCalledWith("/settings", { "mcp.enabled": true }, expect.objectContaining({ params: { scope: "organization" } }));
    await w.get('[data-testid="mcp-port"] input').setValue("8091");
    await w.get('[data-testid="mcp-port-save"]').trigger("click");
    await flushPromises();
    expect(put).toHaveBeenCalledWith("/mcp/server", { listen: "8091" });
    expect(w.get('[data-testid="mcp-dedicated-url"]').text()).toContain(":8091/mcp");
    w.unmount();
  });

  it("reports a bind failure and locks the port for non-owners", async () => {
    mockMcpGets(status({ listening: false, activeAddr: undefined, error: "address already in use", canChange: false }), true, true);
    const w = mount(McpServerPanel, { global: { plugins: [i18n()], stubs: { RouterLink: true } } });
    await flushPromises();
    expect(w.get('[data-testid="mcp-port-error"]').text()).toContain("address already in use");
    expect(w.find('[data-testid="mcp-port-save"]').exists()).toBe(false);
    expect(w.get('[data-testid="mcp-port"] input').attributes("disabled")).toBeDefined();
    expect(w.get('[data-testid="mcp-enabled"] input').attributes("disabled")).toBeDefined();
    w.unmount();
  });
});

describe("MCP helpers", () => {
  it("builds the URLs a client reaches", () => {
    expect(dedicatedUrl("[::]:8090", "/mcp", "mail.example.com")).toBe("http://mail.example.com:8090/mcp");
    expect(dedicatedUrl("0.0.0.0:8090", "/mcp", "mail.example.com")).toBe("http://mail.example.com:8090/mcp");
    expect(dedicatedUrl("127.0.0.1:8090", "/mcp", "mail.example.com")).toBe("http://127.0.0.1:8090/mcp");
    expect(dedicatedUrl("[::1]:8090", "/mcp", "x")).toBe("http://[::1]:8090/mcp");
    expect(mainUrl(undefined, "/v1/mcp", "https://admin.example.com/")).toBe("https://admin.example.com/v1/mcp");
    expect(mainUrl("https://api.example.com/v1/mcp", "/v1/mcp", "https://x")).toBe("https://api.example.com/v1/mcp");
  });

  it("emits valid JSON configs with the key placeholder", () => {
    for (const c of ["claudeDesktop", "cursor", "vscode"] as const) {
      const txt = mcpClientConfig(c, "https://h/v1/mcp");
      expect(() => JSON.parse(txt)).not.toThrow();
      expect(txt).toContain("Bearer <API key>");
      expect(txt).toContain("https://h/v1/mcp");
    }
  });

  it("keeps the saved model in the picker before Connect", () => {
    expect(modelItems([], "claude-haiku-4-5")).toEqual([{ title: "claude-haiku-4-5", value: "claude-haiku-4-5" }]);
    expect(modelItems([{ id: "a", name: "Model A" }], "a")).toEqual([{ title: "Model A (a)", value: "a" }]);
  });
});
