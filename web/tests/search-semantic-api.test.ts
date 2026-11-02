import { beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import aiEn from "@/features/ai/locales/en.json";

const { get, post, put, rawPost } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn(), put: vi.fn(), rawPost: vi.fn() }));

vi.mock("@/api/client", () => ({
  api: { get, post, put, raw: { post: rawPost } },
  ApiProblemError: class extends Error {
    problem: { status: number };
    constructor(p: { status: number }) {
      super("problem");
      this.problem = p;
    }
  },
}));

import {
  askMailbox,
  getEmbeddingsStatus,
  reindexEmbeddings,
  semanticSearch,
} from "@/features/search/helpers/api";
import AiProviderPanel from "@/features/ai/components/AiProviderPanel.vue";

beforeEach(() => {
  get.mockReset();
  post.mockReset();
  put.mockReset();
  rawPost.mockReset();
});

describe("semantic search api helpers", () => {
  it("builds the account routes", async () => {
    get.mockResolvedValue({});
    await getEmbeddingsStatus("acc_1");
    expect(get).toHaveBeenLastCalledWith("/accounts/acc_1/embeddings");
    await semanticSearch({ accountId: "acc_1", q: "next assembly", limit: 100, from: "2026-10-01T00:00:00Z" });
    expect(get).toHaveBeenLastCalledWith(
      "/accounts/acc_1/semantic-search?q=next+assembly&limit=50&from=2026-10-01T00%3A00%3A00Z",
    );
    post.mockResolvedValue({ job: { id: "job_1" } });
    await reindexEmbeddings("acc_1");
    expect(post).toHaveBeenLastCalledWith("/accounts/acc_1/embeddings/reindex");
  });

  it("asks with a longer request timeout", async () => {
    rawPost.mockResolvedValue({ data: { answer: "x", citations: [], excerpts: 1, model: "m" } });
    const res = await askMailbox("acc_1", "When?");
    expect(res.answer).toBe("x");
    expect(rawPost).toHaveBeenCalledWith("/accounts/acc_1/ask", { question: "When?" }, { headers: { "EB-Timeout": "60000" } });
    await askMailbox("acc_1", "When?", { from: "2026-10-01T00:00:00Z", to: "" });
    expect(rawPost).toHaveBeenLastCalledWith(
      "/accounts/acc_1/ask",
      { question: "When?", from: "2026-10-01T00:00:00Z", to: undefined },
      { headers: { "EB-Timeout": "60000" } },
    );
  });
});

describe("AI settings embeddings block", () => {
  const i18n = () =>
    createI18n({ legacy: false, locale: "en", missingWarn: false, fallbackWarn: false, messages: { en: { ...aiEn } } });

  it("shows the effective model and saves the block", async () => {
    get.mockResolvedValue({
      provider: "openai", model: "gpt", features: ["summary"], piiRedact: true, bodyMaxChars: 8000, apiKeySet: true,
      embeddings: { apiKeySet: false, configured: true, effectiveBaseUrl: "https://api.openai.com/v1", effectiveModel: "text-embedding-3-small" },
    });
    const w = mount(AiProviderPanel, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="ai-emb-effective"]').text()).toContain("text-embedding-3-small");

    put.mockResolvedValue({
      provider: "openai", model: "gpt", features: ["summary"], piiRedact: true, bodyMaxChars: 8000, apiKeySet: true,
      embeddings: { model: "text-embedding-3-large", apiKeySet: true, configured: true, effectiveModel: "text-embedding-3-large" },
    });
    await w.find('[data-testid="ai-emb-model"] input').setValue("text-embedding-3-large");
    await w.find('[data-testid="ai-emb-api-key"] input').setValue("sk-emb");
    await (w.vm as unknown as { $nextTick: () => Promise<void> }).$nextTick();
    const save = w.findAll("button").find((b) => b.text().includes("Save AI settings"));
    await save!.trigger("click");
    await flushPromises();
    expect(put).toHaveBeenCalledWith("/ai/settings", expect.objectContaining({
      embeddings: { baseUrl: undefined, model: "text-embedding-3-large", apiKey: "sk-emb" },
    }));
    w.unmount();
  });

  it("explains why semantic search cannot use the settings", async () => {
    get.mockResolvedValue({
      provider: "anthropic", model: "claude", features: ["summary"], piiRedact: true, bodyMaxChars: 8000, apiKeySet: true,
      embeddings: { apiKeySet: false, configured: false, problem: "The Anthropic provider has no embeddings API." },
    });
    const w = mount(AiProviderPanel, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="ai-emb-problem"]').text()).toContain("has no embeddings API");
    w.unmount();
  });
});
