import type { AIProvider } from "@/api/ai";

/** Display order of the provider picker. */
export const AI_PROVIDERS: AIProvider[] = ["none", "openai", "anthropic", "gemini", "xai", "local"];

/** Providers with an embeddings API of their own (semantic search can reuse them). */
export const EMBEDDING_PROVIDERS: AIProvider[] = ["openai", "gemini", "local"];

/** Public API base the backend uses when the base URL is empty (custom has none). */
export const DEFAULT_BASE_URLS: Partial<Record<AIProvider, string>> = {
  openai: "https://api.openai.com/v1",
  anthropic: "https://api.anthropic.com",
  gemini: "https://generativelanguage.googleapis.com",
  xai: "https://api.x.ai/v1",
};

export const hasEmbeddingsApi = (p: AIProvider): boolean => EMBEDDING_PROVIDERS.includes(p);

/** Model picker items: the provider's list plus the saved model, so it shows before Connect. */
export const modelItems = (
  models: Array<{ id: string; name: string }>,
  current: string,
): Array<{ title: string; value: string }> => {
  const items = models.map((m) => ({ title: m.name && m.name !== m.id ? `${m.name} (${m.id})` : m.id, value: m.id }));
  if (current && !models.some((m) => m.id === current)) items.unshift({ title: current, value: current });
  return items;
};
