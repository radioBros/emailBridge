import { computed, ref, type Ref } from "vue";
import {
  countMembers,
  countSandboxSince,
  countWebhookEndpoints,
  getStats,
  type OrgStats,
} from "@/api/dashboard";
import { listQueues, type QueueView } from "@/api/cluster";
import { getLogStats, type LogStats } from "@/api/logs";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import {
  AccountStates,
  DeliveryStates,
  LogLevels,
  OutboundStates,
  type AccountState,
  type DeliveryState,
  type LogLevel,
  type OutboundState,
} from "@/constants/Statuses";
import { useSessionStore } from "@/stores/session";

export interface DashMetric {
  value: number | null;
  error: string | null;
  loading: boolean;
}

export interface AccountStateSegment {
  state: AccountState;
  value: number | null;
  color: string;
  href: string;
  loading: boolean;
  error: string | null;
}

export interface ChartSlice {
  key: string;
  labelKey: string;
  value: number;
  color: string;
}

/** Attention = authError + connectError + disabled. */
export const ATTENTION_STATES: AccountState[] = [
  "authError",
  "connectError",
  "disabled",
];

const accountStates = Object.keys(AccountStates) as AccountState[];
const outboundStates = Object.keys(OutboundStates) as OutboundState[];
const deliveryStates = Object.keys(DeliveryStates) as DeliveryState[];

const emptyMetric = (): DashMetric => ({
  value: null,
  error: null,
  loading: true,
});

const errMessage = (err: unknown): string =>
  problemOf(err)?.detail || problemOf(err)?.title || "error";

const fill = async (
  metric: Ref<DashMetric>,
  fetch: () => Promise<number>,
): Promise<number | null> => {
  metric.value = { ...metric.value, loading: true, error: null };
  try {
    const value = await fetch();
    metric.value = { value, loading: false, error: null };
    return value;
  } catch (err) {
    metric.value = { value: null, loading: false, error: errMessage(err) };
    return null;
  }
};

const sumMap = (m: Record<string, number> | undefined): number =>
  Object.values(m ?? {}).reduce((s, n) => s + (Number(n) || 0), 0);

/**
 * Dashboard counters from GET /v1/stats (plus compact list totals, log stats,
 * and queue depth for charts the API already exposes).
 *
 * Log-levels / queue charts are gated: viewers must not hit endpoints that 403
 * (`logs.read` / `cluster.read`). Match Logs / Cluster screens.
 */
export const useDashboardStats = () => {
  const session = useSessionStore();

  /** Same gate as useLogs — org owner/admin/operator (perm `logs.read`). */
  const canReadLogLevels = computed(() => {
    const perms = session.me?.permissions ?? [];
    if (perms.includes("logs.read")) return true;
    const role = session.me?.role;
    return role === "owner" || role === "admin" || role === "operator";
  });

  /** Same gate as useCluster — instance owner (perm `cluster.read`). */
  const canReadQueues = computed(() => {
    const perms = session.me?.permissions ?? [];
    if (perms.includes("cluster.read") || perms.includes("instance.admin")) {
      return true;
    }
    return session.profile?.instanceRole === "owner";
  });

  const connected = ref<DashMetric>(emptyMetric());
  const accountsTotal = ref<number | null>(null);
  const attention = ref<DashMetric>(emptyMetric());
  const queued = ref<DashMetric>(emptyMetric());
  const failed = ref<DashMetric>(emptyMetric());
  const sandbox = ref<DashMetric>(emptyMetric());
  const webhooks = ref<DashMetric>(emptyMetric());
  const members = ref<DashMetric>(emptyMetric());
  const events24h = ref<DashMetric>(emptyMetric());
  const deadLetters = ref<DashMetric>(emptyMetric());
  const statsError = ref<string | null>(null);
  const stats = ref<OrgStats | null>(null);
  const logStats = ref<LogStats | null>(null);
  const queues = ref<QueueView[]>([]);
  const stateCounts = ref<Record<AccountState, DashMetric>>(
    Object.fromEntries(accountStates.map((s) => [s, emptyMetric()])) as Record<
      AccountState,
      DashMetric
    >,
  );

  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const stateSegments = computed<AccountStateSegment[]>(() =>
    accountStates.map((state) => {
      const m = stateCounts.value[state];
      return {
        state,
        value: m.value,
        color: AccountStates[state].color,
        href: `/accounts?state=${encodeURIComponent(state)}`,
        loading: m.loading,
        error: m.error,
      };
    }),
  );

  const stateTotal = computed(() =>
    stateSegments.value.reduce((sum, s) => sum + Math.max(0, s.value ?? 0), 0),
  );

  const outboundSlices = computed<ChartSlice[]>(() => {
    const map = stats.value?.outboundMessages ?? {};
    return outboundStates
      .map((key) => ({
        key,
        labelKey: `dashboard.outbound.${key}`,
        value: Number(map[key] ?? 0),
        color: OutboundStates[key].color,
      }))
      .filter((s) => s.value > 0);
  });

  const deliverySlices = computed<ChartSlice[]>(() => {
    const map = stats.value?.deliveriesLast24h ?? {};
    return deliveryStates
      .map((key) => ({
        key,
        labelKey: `dashboard.deliveries.${key}`,
        value: Number(map[key] ?? 0),
        color: DeliveryStates[key].color,
      }))
      .filter((s) => s.value > 0);
  });

  const logLevelSlices = computed<ChartSlice[]>(() => {
    const map = logStats.value?.byLevel ?? {};
    return Object.entries(map)
      .map(([key, value]) => {
        const level = key.toLowerCase() as LogLevel;
        const known = level in LogLevels ? LogLevels[level] : null;
        return {
          key,
          labelKey: `dashboard.log_levels.${key}`,
          value: Number(value) || 0,
          color: known?.color ?? "primary",
        };
      })
      .filter((s) => s.value > 0)
      .sort((a, b) => b.value - a.value);
  });

  /** Queue names → i18n keys under `dashboard.queues.*` (unknown names fall back to `key`). */
  const queueSlices = computed<ChartSlice[]>(() =>
    queues.value
      .map((q) => {
        const slug = q.name.replace(/[^a-zA-Z0-9]+/g, "_").replace(/^_|_$/g, "").toLowerCase();
        return {
          key: q.name,
          labelKey: `dashboard.queues.${slug || q.name}`,
          value: Number(q.pending) || 0,
          color: q.paused ? "warning" : "info",
        };
      })
      .filter((s) => s.value > 0)
      .sort((a, b) => b.value - a.value),
  );

  const load = async (): Promise<void> => {
    start();
    connected.value = emptyMetric();
    attention.value = emptyMetric();
    queued.value = emptyMetric();
    failed.value = emptyMetric();
    sandbox.value = emptyMetric();
    webhooks.value = emptyMetric();
    members.value = emptyMetric();
    events24h.value = emptyMetric();
    deadLetters.value = emptyMetric();
    accountsTotal.value = null;
    statsError.value = null;
    stats.value = null;
    logStats.value = null;
    queues.value = [];
    stateCounts.value = Object.fromEntries(
      accountStates.map((s) => [s, emptyMetric()]),
    ) as Record<AccountState, DashMetric>;

    const from = new Date(Date.now() - 24 * 60 * 60 * 1000).toISOString();
    const to = new Date().toISOString();

    try {
      const body = await getStats();
      stats.value = body;

      const accounts = body.accounts ?? {};
      stateCounts.value = Object.fromEntries(
        accountStates.map((state) => [
          state,
          {
            value: Number(accounts[state] ?? 0),
            loading: false,
            error: null,
          },
        ]),
      ) as Record<AccountState, DashMetric>;

      accountsTotal.value = sumMap(accounts);
      connected.value = {
        value: Number(accounts.connected ?? 0),
        loading: false,
        error: null,
      };
      attention.value = {
        value: ATTENTION_STATES.reduce(
          (s, st) => s + Number(accounts[st] ?? 0),
          0,
        ),
        loading: false,
        error: null,
      };

      const outbound = body.outboundMessages ?? {};
      queued.value = {
        value: Number(outbound.queued ?? 0),
        loading: false,
        error: null,
      };
      failed.value = {
        value: Number(outbound.failed ?? 0),
        loading: false,
        error: null,
      };
      events24h.value = {
        value: Number(body.eventsLast24h ?? 0),
        loading: false,
        error: null,
      };
      deadLetters.value = {
        value: Number(body.deadLetters ?? 0),
        loading: false,
        error: null,
      };
    } catch (err) {
      const msg = errMessage(err);
      statsError.value = msg;
      connected.value = { value: null, loading: false, error: msg };
      attention.value = { value: null, loading: false, error: msg };
      queued.value = { value: null, loading: false, error: msg };
      failed.value = { value: null, loading: false, error: msg };
      events24h.value = { value: null, loading: false, error: msg };
      deadLetters.value = { value: null, loading: false, error: msg };
      stateCounts.value = Object.fromEntries(
        accountStates.map((s) => [
          s,
          { value: null, loading: false, error: msg },
        ]),
      ) as Record<AccountState, DashMetric>;
    }

    const extras: Promise<unknown>[] = [
      fill(sandbox, () => countSandboxSince(from)),
      fill(webhooks, () => countWebhookEndpoints()),
      fill(members, () => countMembers()),
    ];
    if (canReadLogLevels.value) {
      extras.push(
        getLogStats(from, to)
          .then((body) => {
            logStats.value = body;
          })
          .catch(() => {
            logStats.value = null;
          }),
      );
    }
    if (canReadQueues.value) {
      extras.push(
        listQueues()
          .then((list) => {
            queues.value = list;
          })
          .catch(() => {
            queues.value = [];
          }),
      );
    }
    await Promise.all(extras);

    const anyOk =
      connected.value.value != null ||
      attention.value.value != null ||
      queued.value.value != null ||
      members.value.value != null;
    finish(anyOk);
  };

  return {
    connected,
    accountsTotal,
    attention,
    queued,
    failed,
    sandbox,
    webhooks,
    members,
    events24h,
    deadLetters,
    statsError,
    stateSegments,
    stateTotal,
    outboundSlices,
    deliverySlices,
    logLevelSlices,
    queueSlices,
    canReadLogLevels,
    canReadQueues,
    isFirstLoad,
    isRefreshing,
    load,
  };
};
