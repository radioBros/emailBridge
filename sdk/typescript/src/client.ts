import { parseProblem, ProblemError } from "./problem.js";
import type { components } from "./generated/openapi.js";

export type Page<T> = {
  data: T[] | null;
  nextCursor: string | null;
  total?: number;
  totalCapped?: boolean;
};

export type ClientOptions = {
  baseUrl: string;
  apiKey: string;
  organizationId?: string;
  fetch?: typeof fetch;
  maxRetries?: number;
  /** Default headers merged into every request. */
  headers?: Record<string, string>;
};

export type RequestOptions = {
  query?: Record<string, string | number | boolean | undefined | null>;
  headers?: Record<string, string>;
  body?: unknown;
  idempotencyKey?: string;
  signal?: AbortSignal;
};

const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));

const parseRetryAfter = (h: Headers, attempt: number): number => {
  const ra = h.get("Retry-After");
  if (ra) {
    const secs = Number(ra);
    if (Number.isFinite(secs)) return Math.max(0, secs * 1000);
    const when = Date.parse(ra);
    if (Number.isFinite(when)) return Math.max(0, when - Date.now());
  }
  return Math.min(30_000, 500 * 2 ** attempt);
};

const buildUrl = (base: string, path: string, query?: RequestOptions["query"]): string => {
  const u = new URL(path.replace(/^\//, ""), base.endsWith("/") ? base : base + "/");
  if (query) {
    for (const [k, v] of Object.entries(query)) {
      if (v === undefined || v === null) continue;
      u.searchParams.set(k, String(v));
    }
  }
  return u.toString();
};

export class Client {
  readonly baseUrl: string;
  readonly apiKey: string;
  readonly organizationId?: string;
  readonly maxRetries: number;
  private readonly fetchImpl: typeof fetch;
  private readonly defaultHeaders: Record<string, string>;

  constructor(opts: ClientOptions) {
    this.baseUrl = opts.baseUrl.replace(/\/+$/, "");
    this.apiKey = opts.apiKey;
    this.organizationId = opts.organizationId;
    this.maxRetries = opts.maxRetries ?? 3;
    this.fetchImpl = opts.fetch ?? fetch.bind(globalThis);
    this.defaultHeaders = opts.headers ?? {};
  }

  async request<T = unknown>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
    const headers: Record<string, string> = {
      Accept: "application/json",
      Authorization: `Bearer ${this.apiKey}`,
      ...this.defaultHeaders,
      ...opts.headers,
    };
    if (this.organizationId && !headers["EB-Organization"]) {
      headers["EB-Organization"] = this.organizationId;
    }
    if (opts.idempotencyKey) headers["Idempotency-Key"] = opts.idempotencyKey;

    let body: string | undefined;
    if (opts.body !== undefined) {
      body = JSON.stringify(opts.body);
      headers["Content-Type"] = headers["Content-Type"] ?? "application/json";
    }

    const url = buildUrl(this.baseUrl, path, opts.query);
    let lastErr: unknown;

    for (let attempt = 0; attempt <= this.maxRetries; attempt++) {
      const res = await this.fetchImpl(url, {
        method,
        headers,
        body,
        signal: opts.signal,
      });

      if (res.status === 429 || res.status === 503) {
        if (attempt >= this.maxRetries) {
          const text = await res.text();
          let parsed: unknown;
          try {
            parsed = text ? JSON.parse(text) : null;
          } catch {
            parsed = null;
          }
          throw new ProblemError(parseProblem(parsed, res.status), res.headers);
        }
        await sleep(parseRetryAfter(res.headers, attempt));
        continue;
      }

      if (res.status === 204 || res.status === 205) {
        return undefined as T;
      }

      const text = await res.text();
      let parsed: unknown = undefined;
      if (text) {
        try {
          parsed = JSON.parse(text);
        } catch {
          parsed = text;
        }
      }

      if (!res.ok) {
        throw new ProblemError(parseProblem(parsed, res.status), res.headers);
      }
      return parsed as T;
    }

    throw lastErr instanceof Error ? lastErr : new Error("request failed");
  }

  get<T = unknown>(path: string, opts?: RequestOptions) {
    return this.request<T>("GET", path, opts);
  }
  post<T = unknown>(path: string, opts?: RequestOptions) {
    return this.request<T>("POST", path, opts);
  }
  patch<T = unknown>(path: string, opts?: RequestOptions) {
    return this.request<T>("PATCH", path, opts);
  }
  put<T = unknown>(path: string, opts?: RequestOptions) {
    return this.request<T>("PUT", path, opts);
  }
  delete<T = unknown>(path: string, opts?: RequestOptions) {
    return this.request<T>("DELETE", path, opts);
  }

  /** Cursor-iterate a list endpoint that returns `{ data, nextCursor }`. */
  async *iterateCursor<T>(
    path: string,
    opts: RequestOptions & { limit?: number } = {},
  ): AsyncGenerator<T, void, void> {
    let cursor: string | null | undefined = opts.query?.cursor != null ? String(opts.query.cursor) : undefined;
    for (;;) {
      const page: Page<T> = await this.get<Page<T>>(path, {
        ...opts,
        query: { ...opts.query, limit: opts.limit, cursor: cursor ?? undefined },
      });
      for (const item of page.data ?? []) yield item;
      if (!page.nextCursor) return;
      cursor = page.nextCursor;
    }
  }

  /** Fetch one page (admin ServerTable style). */
  listPage<T>(path: string, page: number, opts: RequestOptions & { limit?: number } = {}) {
    return this.get<Page<T>>(path, {
      ...opts,
      query: { ...opts.query, page, limit: opts.limit },
    });
  }
}

/** Convenience aliases for generated component schemas. */
export type Schemas = components["schemas"];
