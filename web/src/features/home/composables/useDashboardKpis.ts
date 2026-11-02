import { computed, onScopeDispose, ref, shallowRef } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiAccountAlert,
  mdiAccountMultiple,
  mdiApi,
  mdiApiOff,
  mdiCheckCircle,
  mdiEmailAlert,
  mdiEmailArrowRight,
  mdiEmailArrowLeft,
  mdiHeartPulse,
  mdiMailboxOutline,
  mdiTimerSand,
  mdiTrayFull,
  mdiWebhook,
  mdiWebCancel,
} from "@mdi/js";
import {
  DEFAULT_KPI_PERIOD,
  getKpis,
  MAX_KPI_RANGE_DAYS,
  getSavedKpiSelection,
  isKpiPeriod,
  saveKpiSelection,
  type DashboardKpis,
  type Kpi,
  type KpiChoice,
  type KpiSelection,
} from "@/api/dashboard";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import {
  API_P95_MS,
  BUS_RTT_MS,
  MONGO_PING_MS,
  attentionAccent,
  failureAccent,
  latencyAccent,
  queueAccent,
  worstAccent,
  type HealthAccent,
  type TrendPolarity,
} from "@/features/home/helpers/kpiHealth";
import { EMPTY_VALUE } from "@/helpers/Format";
import { useSessionStore } from "@/stores/session";

/** Background refresh interval (W74 §3). */
export const KPI_POLL_MS = 30_000;

/** One rendered KPI card. */
export interface KpiCardModel {
  id: string;
  label: string;
  icon: string;
  accent: HealthAccent;
  value: string;
  /** Trend + sparkline source; absent for point-in-time cards. */
  kpi?: Kpi;
  polarity: TrendPolarity;
  /** Secondary line (e.g. "2 in progress") for cards without a trend. */
  sub?: string;
  /** Hover text (e.g. the 4xx / 5xx split). */
  tooltip?: string;
  href: string;
}

const QUEUE_CARDS = ["webhooks", "submission", "sysmail"] as const;

const errMessage = (err: unknown): string =>
  problemOf(err)?.detail || problemOf(err)?.title || "error";

/**
 * W74 dashboard KPI cards: period selection (persisted in /v1/me/settings), GET /v1/stats?period=,
 * permission-gated card list, 30 s background polling without flicker.
 *
 * Loading: first load → skeleton (`isFirstLoad`); a period change → overlay (`isRefreshing`); a
 * background poll keeps the previous values on screen and shows nothing.
 */
export const useDashboardKpis = () => {
  const { t, locale } = useI18n();
  const session = useSessionStore();
  const period = ref<KpiChoice>(DEFAULT_KPI_PERIOD);
  /** The custom window (ISO times from the date field); used while period is "custom". */
  const customFrom = ref("");
  const customTo = ref("");

  const selection = (): KpiSelection =>
    period.value === "custom"
      ? { period: "custom", from: customFrom.value, to: customTo.value }
      : { period: period.value };
  const applySelection = (sel: KpiSelection): void => {
    period.value = sel.period;
    if (sel.period === "custom") {
      customFrom.value = sel.from;
      customTo.value = sel.to;
    }
  };
  /** A custom window that ended before now never changes: no need to poll it. */
  const isLive = (): boolean =>
    period.value !== "custom" || !customTo.value || Date.parse(customTo.value) >= Date.now() - 60_000;
  const data = shallowRef<DashboardKpis | null>(null);
  const error = ref<string | null>(null);
  const lastUpdated = ref<Date | null>(null);
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  /** Mirrors the server gate so the first-load skeleton has the right number of cells. */
  const canReadLogs = computed(() => {
    const perms = session.me?.permissions ?? [];
    if (perms.includes("logs.read")) return true;
    const role = session.me?.role;
    return role === "owner" || role === "admin" || role === "operator";
  });
  const isInstanceOwner = computed(() => {
    const perms = session.me?.permissions ?? [];
    if (perms.includes("cluster.read") || perms.includes("instance.admin")) return true;
    return session.profile?.instanceRole === "owner";
  });

  const num = (n: number | null | undefined, digits = 0): string =>
    n == null
      ? EMPTY_VALUE
      : new Intl.NumberFormat(locale.value, { maximumFractionDigits: digits }).format(n);

  let inFlight = false;
  let seq = 0;
  /** The selection of the last foreground request — what Retry asks for again. */
  let requested: KpiSelection = { period: DEFAULT_KPI_PERIOD };

  /** Fetches the KPIs. `mode`: first/refetch go through the load state; `silent` (polling) does not. */
  const fetchKpis = async (mode: "load" | "silent" = "load"): Promise<void> => {
    if (mode === "silent" && (inFlight || !isLive())) return;
    if (period.value === "custom" && (!customFrom.value || !customTo.value)) return;
    const mine = ++seq;
    inFlight = true;
    if (mode === "load") {
      start();
      requested = selection();
    }
    let ok = false;
    try {
      const body = await getKpis(selection());
      if (mine === seq) {
        data.value = body;
        error.value = null;
        lastUpdated.value = new Date();
      }
      ok = true;
    } catch (err) {
      if (mine === seq && mode === "load") {
        error.value = errMessage(err);
        // Keep the toggle honest: the cards still show the last period that loaded.
        if (data.value) {
          period.value = data.value.period as KpiChoice;
          if (data.value.period === "custom") {
            customFrom.value = data.value.from;
            customTo.value = data.value.to;
          }
        }
      }
    } finally {
      if (mine === seq) inFlight = false;
      if (mode === "load" && mine === seq) finish(ok);
    }
  };

  /** Restores the saved period (never fails), then loads. */
  const init = async (): Promise<void> => {
    start(); // the skeleton covers the settings read too
    try {
      applySelection(await getSavedKpiSelection());
    } catch {
      period.value = DEFAULT_KPI_PERIOD;
    }
    await fetchKpis("load");
  };

  const persist = (): void => {
    saveKpiSelection(selection()).catch(() => {
      /* non-fatal: the choice still applies to this visit */
    });
  };

  /** A preset loads at once; "custom" shows the date field and loads once a window is picked (or at once
   * when one was picked before). */
  const setPeriod = (p: unknown): void => {
    if (p === period.value) return;
    if (p === "custom") {
      period.value = "custom";
      if (customFrom.value && customTo.value) {
        persist();
        void fetchKpis("load");
      }
      return;
    }
    if (!isKpiPeriod(p)) return;
    period.value = p;
    persist();
    void fetchKpis("load");
  };

  /** A window picked in the date field (both ends set); at most MAX_KPI_RANGE_DAYS days. */
  const setCustomRange = (from: string, to: string): void => {
    if (!from || !to) return;
    const days = (Date.parse(to) - Date.parse(from)) / 86_400_000;
    if (!(days > 0) || days > MAX_KPI_RANGE_DAYS + 1) return;
    period.value = "custom";
    customFrom.value = from;
    customTo.value = to;
    persist();
    void fetchKpis("load");
  };

  let timer: ReturnType<typeof setInterval> | null = null;
  const onVisible = (): void => {
    if (typeof document !== "undefined" && document.visibilityState === "visible" && data.value) {
      void fetchKpis("silent");
    }
  };
  const startPolling = (): void => {
    if (timer) return;
    timer = setInterval(() => {
      if (typeof document !== "undefined" && document.visibilityState === "hidden") return;
      void fetchKpis("silent");
    }, KPI_POLL_MS);
    if (typeof document !== "undefined") document.addEventListener("visibilitychange", onVisible);
  };
  const stopPolling = (): void => {
    if (timer) clearInterval(timer);
    timer = null;
    if (typeof document !== "undefined") document.removeEventListener("visibilitychange", onVisible);
  };
  onScopeDispose(stopPolling);

  /** Query string of the current window for list click-throughs. */
  const range = computed(() => {
    const d = data.value;
    if (!d) return "";
    return `from=${encodeURIComponent(d.from)}&to=${encodeURIComponent(d.to)}`;
  });

  /** The same window for the Outbox, whose date range is createdFrom / createdTo (from / to are addresses). */
  const outboxRange = computed(() => {
    const d = data.value;
    if (!d) return "";
    return `createdFrom=${encodeURIComponent(d.from)}&createdTo=${encodeURIComponent(d.to)}`;
  });

  const trendCard = (
    id: string,
    kpi: Kpi,
    label: string,
    icon: string,
    accent: HealthAccent,
    polarity: TrendPolarity,
    href: string,
    tooltip?: string,
  ): KpiCardModel => ({ id, label, icon, accent, value: num(kpi.current), kpi, polarity, href, tooltip });

  /** Card count the skeleton shows before data exists (same gate as the server). */
  const expectedCount = computed(() => {
    let n = 8;
    if (canReadLogs.value) n += 2;
    if (isInstanceOwner.value) n += QUEUE_CARDS.length;
    if (isInstanceOwner.value || canReadLogs.value) n += 1;
    return n;
  });

  const cards = computed<KpiCardModel[]>(() => {
    const d = data.value;
    if (!d) return [];
    const r = range.value;
    const o = outboxRange.value;
    const out: KpiCardModel[] = [
      {
        id: "accountsTotal",
        label: t("dashboard.kpis.accounts_total"),
        icon: mdiAccountMultiple,
        accent: "primary",
        value: num(d.accounts.total),
        polarity: "neutral",
        sub: t("dashboard.kpis.point_in_time"),
        href: "/accounts",
      },
      {
        id: "accountsConnected",
        label: t("dashboard.kpis.accounts_connected"),
        icon: mdiMailboxOutline,
        accent: d.accounts.total > 0 && d.accounts.connected === d.accounts.total ? "success" : "info",
        value: num(d.accounts.connected),
        polarity: "neutral",
        sub: t("dashboard.kpis.of_total", { total: num(d.accounts.total) }),
        href: "/accounts?state=connected",
      },
      {
        id: "accountsAttention",
        label: t("dashboard.kpis.accounts_attention"),
        icon: d.accounts.attention > 0 ? mdiAccountAlert : mdiCheckCircle,
        accent: attentionAccent(d.accounts.attention),
        value: num(d.accounts.attention),
        polarity: "bad",
        sub: t("dashboard.kpis.attention_hint"),
        href: "/accounts",
      },
      trendCard("messagesReceived", d.messagesReceived, t("dashboard.kpis.messages_received"), mdiEmailArrowLeft,
        "info", "neutral", "/messages"),
      trendCard("messagesSent", d.messagesSent, t("dashboard.kpis.messages_sent"), mdiEmailArrowRight,
        "info", "neutral", `/outbox?state=sent&${o}`),
      trendCard("messagesFailed", d.messagesFailed, t("dashboard.kpis.messages_failed"), mdiEmailAlert,
        failureAccent(d.messagesFailed.current, d.messagesSent.current), "bad", `/outbox?state=failed&${o}`),
      trendCard("webhooksDelivered", d.webhooksDelivered, t("dashboard.kpis.webhooks_delivered"), mdiWebhook,
        "info", "neutral", `/deliveries?state=succeeded&${r}`),
      trendCard("webhooksFailed", d.webhooksFailed, t("dashboard.kpis.webhooks_failed"), mdiWebCancel,
        failureAccent(d.webhooksFailed.current, d.webhooksDelivered.current), "bad", `/deliveries?state=${encodeURIComponent("failed,deadLettered")}&${r}`),
    ];
    const api = d.apiCalls;
    if (api) {
      out.push(
        trendCard("apiSucceeded", api.succeeded, t("dashboard.kpis.api_succeeded"), mdiApi,
          "info", "neutral", `/logs?category=api&${r}`),
        trendCard("apiFailed", api.failed, t("dashboard.kpis.api_failed"), mdiApiOff,
          failureAccent(api.serverErrors, api.succeeded.current + api.clientErrors), "bad",
          `/logs?category=api&level=warn&${r}`,
          t("dashboard.kpis.api_split", { client: num(api.clientErrors), server: num(api.serverErrors) })),
      );
    }
    for (const key of QUEUE_CARDS) {
      const q = d.queues?.find((x) => x.key === key);
      if (!q) continue;
      out.push({
        id: `queue-${key}`,
        label: t(`dashboard.kpis.queue_${key}`),
        icon: mdiTrayFull,
        accent: queueAccent(q.waiting, q.paused),
        value: num(q.waiting),
        polarity: "bad",
        sub: q.paused
          ? t("dashboard.kpis.queue_paused", { n: num(q.inProgress) })
          : t("dashboard.kpis.queue_in_progress", { n: num(q.inProgress) }),
        tooltip: t("dashboard.kpis.queue_hint"),
        href: "/cluster",
      });
    }
    if (d.infra) {
      const mongo = latencyAccent(d.infra.mongoPingMs, MONGO_PING_MS, !!d.infra.mongoError);
      const busA = latencyAccent(d.infra.busRttMs, BUS_RTT_MS, !!d.infra.busError);
      out.push({
        id: "health",
        label: t("dashboard.kpis.health"),
        icon: mdiHeartPulse,
        accent: worstAccent(mongo, busA),
        value: d.infra.mongoError
          ? t(`dashboard.kpis.probe_${d.infra.mongoError}`)
          : t("dashboard.kpis.ms", { ms: num(d.infra.mongoPingMs, 1) }),
        polarity: "bad",
        sub: d.infra.busError
          ? t("dashboard.kpis.bus_failed", { reason: t(`dashboard.kpis.probe_${d.infra.busError}`) })
          : t("dashboard.kpis.bus_rtt", { ms: num(d.infra.busRttMs, 1) }),
        tooltip: t("dashboard.kpis.health_hint"),
        href: "/cluster",
      });
    } else if (api) {
      out.push({
        id: "health",
        label: t("dashboard.kpis.api_p95"),
        icon: mdiTimerSand,
        accent: latencyAccent(api.latencyP95Ms, API_P95_MS),
        value: api.latencyP95Ms == null ? EMPTY_VALUE : t("dashboard.kpis.ms", { ms: num(api.latencyP95Ms) }),
        polarity: "bad",
        sub: t("dashboard.kpis.api_p95_hint"),
        href: `/logs?category=api&${r}`,
      });
    }
    return out;
  });

  const historyLimited = computed(() => {
    const d = data.value;
    if (!d) return false;
    return [d.messagesReceived, d.messagesSent, d.messagesFailed, d.webhooksDelivered, d.webhooksFailed].some(
      (k) => k.partial || k.previous == null,
    );
  });

  return {
    period,
    data,
    error,
    lastUpdated,
    cards,
    expectedCount,
    historyLimited,
    isFirstLoad,
    isRefreshing,
    init,
    load: () => fetchKpis("load"),
    /** Retries the period that last failed (the toggle may have reverted to the shown one). */
    retry: () => {
      applySelection(requested);
      return fetchKpis("load");
    },
    poll: () => fetchKpis("silent"),
    setPeriod,
    setCustomRange,
    customFrom,
    customTo,
    startPolling,
    stopPolling,
  };
};
