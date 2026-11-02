import { api } from "@/api/client";

/** A match inside a snippet: [start, end) in Unicode code points. Local until schema regen. */
export interface SearchHighlight {
  start: number;
  end: number;
}

/** One GET /v1/messages/search result. Local until schema regen. */
export interface SearchHit {
  messageId: string;
  accountId: string;
  mailboxId: string;
  path: string;
  subject: string;
  from: string;
  to: string[];
  date: string;
  size: number;
  snippet: string;
  highlights: SearchHighlight[];
  score: number;
}

/** GET /v1/messages/search body. Local until schema regen. */
export interface SearchResult {
  data: SearchHit[];
  total: number;
  totalCapped: boolean;
}

export interface SearchQuery {
  q: string;
  accountId?: string;
  from?: string;
  to?: string;
  page?: number;
  limit?: number;
}

/** The 202 body of POST …/fulltext/reindex. Local until schema regen. */
export interface ReindexAccepted {
  job: { id: string; kind: string; state: string; createdAt?: string };
}

/** The feature switch this feature depends on (internal/fulltext.SwitchName). */
export const FULLTEXT_SWITCH = "search.fullText";

const qs = (params: Record<string, string | number | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

/** GET /v1/messages/search — organisation-wide full-text search. */
export const searchMessages = (query: SearchQuery): Promise<SearchResult> =>
  api.get<SearchResult>(
    `/messages/search${qs({
      q: query.q,
      accountId: query.accountId,
      from: query.from,
      to: query.to,
      page: query.page ?? 1,
      limit: query.limit ?? 25,
    })}`,
  );

/** POST /v1/accounts/{id}/fulltext/reindex — queue (or return the running) reindex job. */
export const reindexAccount = (accountId: string): Promise<ReindexAccepted> =>
  api.post<ReindexAccepted>(`/accounts/${encodeURIComponent(accountId)}/fulltext/reindex`);

/** The account's own `search.fullText` value, or undefined when it inherits the organisation's. */
export const fullTextSwitchOf = (switches: Record<string, unknown> | null | undefined): boolean | undefined => {
  const v = switches?.[FULLTEXT_SWITCH];
  return typeof v === "boolean" ? v : undefined;
};

/* ---------- G1 semantic search and ask (internal/embeddings) ---------- */

/** The feature switch of semantic search (internal/embeddings.SwitchName). */
export const EMBEDDINGS_SWITCH = "ai.embeddings";

/** Why semantic search is not available for an account. Local until schema regen. */
export type EmbeddingsReason = "switch_off" | "fulltext_off" | "ai_off" | "not_configured";

/** GET /v1/accounts/{id}/embeddings. Local until schema regen. */
export interface EmbeddingsStatus {
  accountId: string;
  enabled: boolean;
  reason?: EmbeddingsReason;
  model?: string;
  messages: number;
  chunks: number;
  indexedMessages: number;
  maxChunks: number;
}

/** One GET /v1/accounts/{id}/semantic-search result. Local until schema regen. */
export interface SemanticHit {
  messageId: string;
  accountId: string;
  mailboxId: string;
  path: string;
  subject: string;
  from: string;
  date: string;
  snippet: string;
  score: number;
}

/** GET /v1/accounts/{id}/semantic-search body. Local until schema regen. */
export interface SemanticResult {
  data: SemanticHit[];
  model: string;
  chunks: number;
}

export interface SemanticQuery {
  accountId: string;
  q: string;
  limit?: number;
  from?: string;
  to?: string;
}

/** A message an answer cites. Local until schema regen. */
export interface AskCitation {
  messageId: string;
  accountId: string;
  mailboxId: string;
  subject: string;
  from: string;
  date: string;
  score: number;
}

/** POST /v1/accounts/{id}/ask body. Local until schema regen. */
export interface AskResult {
  answer: string;
  citations: AskCitation[];
  excerpts: number;
  model: string;
}

/** Most results one semantic search returns (internal/embeddings maxSearchLimit). */
export const SEMANTIC_MAX_LIMIT = 50;

/** Ask may take longer than the default request timeout (the server bounds it to 50 s). */
const ASK_TIMEOUT_MS = 60000;

const accountPath = (accountId: string): string => `/accounts/${encodeURIComponent(accountId)}`;

/** GET /v1/accounts/{id}/embeddings — whether semantic search is available and how much is embedded. */
export const getEmbeddingsStatus = (accountId: string): Promise<EmbeddingsStatus> =>
  api.get<EmbeddingsStatus>(`${accountPath(accountId)}/embeddings`);

/** GET /v1/accounts/{id}/semantic-search — the account's messages closest in meaning to q. */
export const semanticSearch = (query: SemanticQuery): Promise<SemanticResult> =>
  api.get<SemanticResult>(
    `${accountPath(query.accountId)}/semantic-search${qs({
      q: query.q,
      limit: Math.min(query.limit ?? 10, SEMANTIC_MAX_LIMIT),
      from: query.from,
      to: query.to,
    })}`,
  );

/** POST /v1/accounts/{id}/ask — an answer from the account's mail with the messages it cites. */
export const askMailbox = async (
  accountId: string,
  question: string,
  range: { from?: string; to?: string } = {},
): Promise<AskResult> => {
  const res = await api.raw.post<AskResult>(
    `${accountPath(accountId)}/ask`,
    { question, from: range.from || undefined, to: range.to || undefined },
    { headers: { "EB-Timeout": String(ASK_TIMEOUT_MS) } },
  );
  return res.data;
};

/** POST /v1/accounts/{id}/embeddings/reindex — queue (or return the running) embeddings job. */
export const reindexEmbeddings = (accountId: string): Promise<ReindexAccepted> =>
  api.post<ReindexAccepted>(`${accountPath(accountId)}/embeddings/reindex`);
