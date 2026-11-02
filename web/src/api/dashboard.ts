import { api, ApiProblemError } from "@/api/client";
import type { components } from "@/api/schema";

export interface ListTotalParams {
  state?: string;
  from?: string;
  to?: string;
  q?: string;
}

export type OrgStats = components["schemas"]["StatsView"];
export type Kpi = components["schemas"]["KPI"];
export type ApiCallKpis = components["schemas"]["APICallKPIs"];
export type QueueKpi = components["schemas"]["QueueKPI"];
export type InfraKpis = components["schemas"]["InfraKPIs"];
export type DashboardKpis = components["schemas"]["KPIs"];

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

/** Organisation operational stats (accounts, outbound, deliveries 24h, events, dead letters). */
export const getStats = (): Promise<OrgStats> => api.get<OrgStats>("/stats");

/** Count via page mode + includeTotal (limit=1). Used for compact cards not on /v1/stats. */
export const countList = async (
  path: string,
  extra: ListTotalParams = {},
): Promise<number> => {
  const body = await api.get<{ total?: number }>(
    `${path}${qs({
      limit: 1,
      page: 1,
      includeTotal: true,
      ...extra,
    })}`,
  );
  return typeof body.total === "number" ? body.total : 0;
};

export const countAccounts = (state?: string): Promise<number> =>
  countList("/accounts", state ? { state } : {});

export const countOutbound = (state?: string): Promise<number> =>
  countList("/outbound-messages", state ? { state } : {});

export const countSandboxSince = (from: string): Promise<number> =>
  countList("/sandbox/messages", { from });

export const countWebhookEndpoints = (): Promise<number> =>
  countList("/webhook-endpoints");

export const countMembers = (): Promise<number> => countList("/members");

/** KPI periods of GET /v1/stats?period= (W74 §1.1). */
export const KPI_PERIODS = ["1h", "24h", "7d", "30d"] as const;
export type KpiPeriod = (typeof KPI_PERIODS)[number];
export const DEFAULT_KPI_PERIOD: KpiPeriod = "24h";

export const isKpiPeriod = (v: unknown): v is KpiPeriod =>
  typeof v === "string" && (KPI_PERIODS as readonly string[]).includes(v);

/** Longest custom window the API accepts (GET /v1/stats?from=&to=). */
export const MAX_KPI_RANGE_DAYS = 92;

/** A preset period, or a custom window (ISO times; `to` may lie in the future — it counts up to now). */
export type KpiSelection =
  | { period: KpiPeriod }
  | { period: "custom"; from: string; to: string };

export type KpiChoice = KpiPeriod | "custom";

const isIsoTime = (v: unknown): v is string => typeof v === "string" && !Number.isNaN(Date.parse(v));

/** Dashboard KPIs for a period or custom window (W74). Throws when the API omits `kpis` (stale node). */
export const getKpis = async (sel: KpiSelection): Promise<DashboardKpis> => {
  const query = sel.period === "custom" ? { from: sel.from, to: sel.to } : { period: sel.period };
  const body = await api.get<OrgStats>(`/stats${qs(query)}`);
  if (!body.kpis) {
    throw new ApiProblemError({
      status: 502,
      code: "kpis_missing",
      title: "Dashboard KPIs unavailable",
      detail: "The stats response did not include kpis. Rebuild or upgrade the API node.",
    });
  }
  return body.kpis;
};

/** The saved dashboard period or custom window from /v1/me/settings (falls back to 24h). */
export const getSavedKpiSelection = async (): Promise<KpiSelection> => {
  const body = await api.get<{ settings?: { dashboard?: { period?: unknown; from?: unknown; to?: unknown } } }>(
    "/me/settings",
  );
  const d = body?.settings?.dashboard;
  if (d?.period === "custom" && isIsoTime(d.from) && isIsoTime(d.to)) {
    return { period: "custom", from: d.from, to: d.to };
  }
  return { period: isKpiPeriod(d?.period) ? d.period : DEFAULT_KPI_PERIOD };
};

/** Persists the dashboard selection (deep-merged server side; other settings untouched). */
export const saveKpiSelection = (sel: KpiSelection): Promise<unknown> =>
  api.patch("/me/settings", {
    dashboard: sel.period === "custom"
      ? { period: "custom", from: sel.from, to: sel.to }
      : { period: sel.period, from: null, to: null },
  });
