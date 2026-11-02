import { api } from "@/api/client";

// local until schema regen (P6.5)

export type HealthSeverity = "ok" | "info" | "warning" | "critical";

export type HealthHint = {
  code: string;
  severity: HealthSeverity | string;
  title: string;
  detail: string;
  docsPath?: string;
};

export type QuotaFact = {
  used?: number;
  limit?: number;
};

export type ProtocolFacts = {
  tlsMode?: string;
  authUser?: string;
  hasPassword?: boolean;
  oauthLinked?: boolean;
  provider?: string;
  idle?: boolean;
  condstore?: boolean;
  qresync?: boolean;
  authMechanisms?: string[];
  quota?: QuotaFact;
  folderCount?: number;
  lastSyncAt?: string;
  lastErrorAt?: string;
  connectLatencyMs?: number;
};

export type HealthRecentError = {
  at: string;
  kind: string;
  code: string;
  protocol?: string;
  response?: string;
  hint?: string;
};

export type LiveCheckError = {
  code?: string;
  kind?: string;
  message?: string;
};

export type LiveCheck = {
  ok: boolean;
  latencyMs?: number;
  capabilities?: string[];
  error?: LiveCheckError;
};

export type StateReason = {
  kind: string;
  code: string;
  message?: string;
  at: string;
};

export type AccountHealth = {
  accountId: string;
  name: string;
  email: string;
  state: string;
  stateReason?: StateReason;
  severity: HealthSeverity | string;
  summary: string;
  hints: HealthHint[];
  facts: ProtocolFacts;
  recentErrors?: HealthRecentError[];
  liveCheck?: LiveCheck;
  checkedAt: string;
};

export type AccountHealthCounts = {
  ok: number;
  info: number;
  warning: number;
  critical: number;
  total: number;
};

export type AccountHealthRow = {
  accountId: string;
  name: string;
  email: string;
  state: string;
  severity: HealthSeverity | string;
  summary: string;
  hintCodes: string[];
  lastSyncAt?: string;
  lastErrorAt?: string;
};

export type AccountHealthList = {
  counts: AccountHealthCounts;
  data: AccountHealthRow[];
  total?: number | null;
  page: number;
  limit: number;
};

export type ListAccountHealthParams = {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  severity?: HealthSeverity | string;
  q?: string;
};

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

export const getAccountHealth = (accountId: string): Promise<AccountHealth> =>
  api.get(`/accounts/${accountId}/health`);

export const checkAccountHealth = (accountId: string): Promise<AccountHealth> =>
  api.post(`/accounts/${accountId}/health/check`);

export const listAccountHealth = (
  params: ListAccountHealthParams = {},
): Promise<AccountHealthList> =>
  api.get(
    `/health/accounts${qs({
      page: params.page,
      limit: params.limit,
      includeTotal: params.includeTotal,
      sort: params.sort,
      severity: params.severity,
      q: params.q,
    })}`,
  );
