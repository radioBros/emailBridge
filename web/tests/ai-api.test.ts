import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn();
const put = vi.fn();

vi.mock("@/api/client", () => ({
  api: { get, put },
}));

describe("ai api", () => {
  beforeEach(() => {
    get.mockReset();
    put.mockReset();
  });

  it("gets and puts settings", async () => {
    const { getAISettings, putAISettings } = await import("@/api/ai");
    get.mockResolvedValue({
      provider: "openai",
      model: "gpt",
      features: ["summary"],
      piiRedact: true,
      bodyMaxChars: 8000,
      apiKeySet: false,
    });
    put.mockResolvedValue({
      provider: "local",
      model: "llama",
      baseUrl: "http://127.0.0.1:11434/v1",
      features: ["summary"],
      piiRedact: true,
      bodyMaxChars: 4000,
      apiKeySet: true,
    });
    const got = await getAISettings();
    expect(got.provider).toBe("openai");
    expect(get).toHaveBeenCalledWith("/ai/settings");
    const saved = await putAISettings({
      provider: "local",
      model: "llama",
      baseUrl: "http://127.0.0.1:11434/v1",
      apiKey: "sk",
    });
    expect(saved.apiKeySet).toBe(true);
    expect(put).toHaveBeenCalledWith("/ai/settings", expect.objectContaining({ provider: "local" }));
  });
});
