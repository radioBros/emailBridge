import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type LogRecord = components["schemas"]["LogRecord"];
export type LogRecordList = components["schemas"]["LogRecordList"];
export type LogSettings = components["schemas"]["LogSettings"];
export type LogStats = components["schemas"]["LogStats"];
export type LogLevel = NonNullable<LogRecord["level"]>;

export interface LogListQuery {
  from: string;
  to: string;
  limit?: number;
  cursor?: string;
  category?: string[];
  level?: LogLevel;
  accountId?: string;
  requestId?: string;
  eventId?: string;
  deliveryId?: string;
  outboundId?: string;
  traceId?: string;
  q?: string;
  organizationId?: string;
}

export interface LogSettingsResult {
  body: LogSettings;
  etag: string | null;
}

const etagOf = (headers: Headers | Record<string, string> | undefined): string | null => {
  if (!headers) return null;
  if (headers instanceof Headers) return headers.get("ETag") ?? headers.get("etag");
  return (headers as Record<string, string>).ETag ?? (headers as Record<string, string>).etag ?? null;
};

const cleanParams = (q: Record<string, unknown>): Record<string, unknown> => {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(q)) {
    if (v === undefined || v === null || v === "") continue;
    if (Array.isArray(v) && v.length === 0) continue;
    out[k] = v;
  }
  return out;
};

/** GET /v1/logs — cursor list, newest first. */
export const listLogs = (query: LogListQuery): Promise<LogRecordList> =>
  api.raw
    .get<LogRecordList>("/logs", { params: cleanParams(query as unknown as Record<string, unknown>) })
    .then((r) => r.data);

/** GET /v1/logs/{id} */
export const getLog = (logId: string): Promise<LogRecord> =>
  api.get<LogRecord>(`/logs/${encodeURIComponent(logId)}`);

/** GET /v1/logs/stats */
export const getLogStats = (from: string, to: string): Promise<LogStats> =>
  api.raw.get<LogStats>("/logs/stats", { params: { from, to } }).then((r) => r.data);

/** GET /v1/logs/settings */
export const getLogSettings = async (): Promise<LogSettingsResult> => {
  const res = await api.raw.get<LogSettings>("/logs/settings");
  return { body: res.data, etag: etagOf(res.headers as Headers) };
};

/** PATCH /v1/logs/settings */
export const patchLogSettings = async (
  body: LogSettings,
  ifMatch?: string | null,
): Promise<LogSettingsResult> => {
  const res = await api.raw.patch<LogSettings>("/logs/settings", body, {
    headers: ifMatch ? { "If-Match": ifMatch } : undefined,
  });
  return { body: res.data, etag: etagOf(res.headers as Headers) };
};

/** GET /v1/logs/export → download NDJSON blob. */
export const exportLogs = async (query: Omit<LogListQuery, "limit" | "cursor">): Promise<Blob> => {
  const res = await api.raw.get<Blob>("/logs/export", {
    params: cleanParams(query as unknown as Record<string, unknown>),
    responseType: "blob",
    headers: { Accept: "application/x-ndjson" },
  });
  return res.data;
};

export interface AccountOption {
  id: string;
  name: string;
  email?: string;
}

/** Lightweight account search for filters (owned by logs feature). */
export const searchAccounts = async (q: string): Promise<AccountOption[]> => {
  const res = await api.raw.get<{ data?: Array<{ id: string; name?: string; email?: string }> }>(
    "/accounts",
    { params: cleanParams({ q, limit: 25, includeTotal: false }) },
  );
  return (res.data.data ?? []).map((a) => ({
    id: a.id,
    name: a.name || a.email || a.id,
    email: a.email,
  }));
};

/** Build the SSE URL for live tail (caller opens with fetch + org header). */
export const logsStreamPath = (query: {
  category?: string[];
  level?: LogLevel;
  accountId?: string;
  q?: string;
}): string => {
  const params = new URLSearchParams();
  for (const c of query.category ?? []) params.append("category", c);
  if (query.level) params.set("level", query.level);
  if (query.accountId) params.set("accountId", query.accountId);
  if (query.q) params.set("q", query.q);
  const qs = params.toString();
  return `/v1/logs/stream${qs ? `?${qs}` : ""}`;
};
