import { api } from "@/api/client";

/** `local` = custom / local OpenAI-compatible server; `none` = AI features off. local until schema regen */
export type AIProvider = "none" | "openai" | "anthropic" | "gemini" | "xai" | "local";

/** local until schema regen */
export type AIFeature = "summary" | "classification" | "extraction" | "risk";

/** The embeddings endpoint of semantic search (G1). local until schema regen */
export interface AIEmbeddings {
  baseUrl?: string;
  model?: string;
  apiKeySet: boolean;
  configured: boolean;
  effectiveBaseUrl?: string;
  effectiveModel?: string;
  problem?: string;
}

/** Replaces the embeddings block; apiKey: omit to keep, "" to remove. local until schema regen */
export interface AIEmbeddingsPut {
  baseUrl?: string;
  model?: string;
  apiKey?: string;
}

/** local until schema regen */
export interface AISettings {
  provider: AIProvider;
  baseUrl?: string;
  model: string;
  features: AIFeature[];
  piiRedact: boolean;
  bodyMaxChars: number;
  apiKeySet: boolean;
  /** Providers with a stored key (the selected one included). */
  providerKeysSet?: AIProvider[];
  embeddings?: AIEmbeddings;
  updatedAt?: string;
}

/** local until schema regen */
export interface AISettingsPut {
  provider: AIProvider;
  baseUrl?: string;
  model?: string;
  apiKey?: string;
  features?: AIFeature[];
  piiRedact?: boolean;
  bodyMaxChars?: number;
  embeddings?: AIEmbeddingsPut;
}

export const getAISettings = async (): Promise<AISettings> =>
  api.get<AISettings>("/ai/settings");

export const putAISettings = async (body: AISettingsPut): Promise<AISettings> =>
  api.put<AISettings>("/ai/settings", body);

/** One entry of a provider's model list. local until schema regen */
export interface AIModel {
  id: string;
  name: string;
}

/** POST /v1/ai/models body; omit apiKey to use the key stored for the provider. local until schema regen */
export interface AIModelsRequest {
  provider: Exclude<AIProvider, "none">;
  baseUrl?: string;
  apiKey?: string;
  kind?: "chat" | "embeddings";
}

/** Connect: verifies the key against the provider and returns its models. */
export const listAIModels = async (body: AIModelsRequest): Promise<AIModel[]> =>
  (await api.post<{ models: AIModel[] }>("/ai/models", body)).models ?? [];
