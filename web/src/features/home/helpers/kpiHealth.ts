/**
 * Dashboard KPI health thresholds and trend maths (W74 §3) — the one place the thresholds live.
 *
 * Accents are Vuetify theme tokens. `neutral` metrics (volumes) never turn red or green by themselves;
 * only failure, backlog and latency metrics carry a health colour.
 */

export type HealthAccent = "success" | "warning" | "error" | "info" | "primary" | "secondary";

/** Failure ratio thresholds: ≤ 1 % healthy, ≤ 5 % warning, above error. */
export const FAILURE_RATIO = { ok: 0.01, warn: 0.05 } as const;
/** Queue backlog (waiting messages). */
export const QUEUE_WAITING = { ok: 100, warn: 1000 } as const;
/** MongoDB ping, ms. */
export const MONGO_PING_MS = { ok: 50, warn: 250 } as const;
/** Message-bus round trip, ms. */
export const BUS_RTT_MS = { ok: 20, warn: 100 } as const;
/** API p95 latency, ms. */
export const API_P95_MS = { ok: 500, warn: 2000 } as const;

const byLimits = (value: number, limits: { ok: number; warn: number }, strict = true): HealthAccent => {
  if (strict ? value < limits.ok : value <= limits.ok) return "success";
  if (strict ? value < limits.warn : value <= limits.warn) return "warning";
  return "error";
};

/** Failed share of all outcomes; no traffic is neutral. */
export const failureAccent = (failed: number, succeeded: number, neutral: HealthAccent = "secondary"): HealthAccent => {
  const total = failed + succeeded;
  if (total <= 0) return neutral;
  return byLimits(failed / total, FAILURE_RATIO, false);
};

export const attentionAccent = (attention: number): HealthAccent => (attention > 0 ? "error" : "success");

export const queueAccent = (waiting: number, paused: boolean): HealthAccent => {
  const base = byLimits(waiting, QUEUE_WAITING);
  return paused && base === "success" ? "warning" : base;
};

export const latencyAccent = (
  ms: number | null | undefined,
  limits: { ok: number; warn: number },
  failed = false,
): HealthAccent => {
  if (failed) return "error";
  if (ms == null) return "secondary";
  return byLimits(ms, limits);
};

const rank: Record<HealthAccent, number> = { success: 0, info: 0, primary: 0, secondary: 0, warning: 1, error: 2 };

/** The worse of two accents. */
export const worstAccent = (a: HealthAccent, b: HealthAccent): HealthAccent => (rank[b] > rank[a] ? b : a);

/** How a rise should be read: `bad` (failures: up is red) or `neutral` (volumes). */
export type TrendPolarity = "bad" | "neutral";

export interface Trend {
  /** `none`: previous period not retained; `new`: previous was 0. */
  kind: "none" | "new" | "flat" | "up" | "down";
  /** Rounded absolute change in percent (up / down only). */
  pct: number;
  color: "error" | "success" | "medium-emphasis";
}

export const computeTrend = (current: number, previous: number | null | undefined, polarity: TrendPolarity): Trend => {
  if (previous == null) return { kind: "none", pct: 0, color: "medium-emphasis" };
  if (previous === 0) {
    if (current === 0) return { kind: "flat", pct: 0, color: "medium-emphasis" };
    return { kind: "new", pct: 0, color: polarity === "bad" ? "error" : "medium-emphasis" };
  }
  const raw = ((current - previous) / previous) * 100;
  const pct = Math.round(Math.abs(raw));
  if (pct === 0) return { kind: "flat", pct: 0, color: "medium-emphasis" };
  const kind = raw > 0 ? "up" : "down";
  if (polarity === "neutral") return { kind, pct, color: "medium-emphasis" };
  return { kind, pct, color: kind === "up" ? "error" : "success" };
};
