import { createPinia, setActivePinia } from "pinia";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { effectScope } from "vue";
import { createI18n } from "vue-i18n";
import { mount, flushPromises } from "@vue/test-utils";
import { createVuetify } from "vuetify";
import * as components from "vuetify/components";
import * as directives from "vuetify/directives";
import en from "@/features/home/locales/en.json";
import KpiCard from "@/components/cards/KpiCard.vue";
import KpiTrend from "@/features/home/components/KpiTrend.vue";
import * as dash from "@/api/dashboard";
import type { DashboardKpis } from "@/api/dashboard";
import {
  attentionAccent,
  computeTrend,
  failureAccent,
  latencyAccent,
  queueAccent,
  worstAccent,
  API_P95_MS,
  MONGO_PING_MS,
} from "@/features/home/helpers/kpiHealth";
import { KPI_POLL_MS, useDashboardKpis } from "@/features/home/composables/useDashboardKpis";
import { useSessionStore } from "@/stores/session";

vi.mock("@/api/dashboard", async (orig) => {
  const real = await orig<typeof import("@/api/dashboard")>();
  return {
    ...real,
    getKpis: vi.fn(),
    getSavedKpiSelection: vi.fn(async () => ({ period: "7d" })),
    saveKpiSelection: vi.fn(async () => ({})),
  };
});

vi.mock("@/api/auth", () => ({
  auth: {
    useSession: () => ({ value: { data: null, isPending: false } }),
    signout: vi.fn(),
  },
}));

const i18n = createI18n({ legacy: false, locale: "en", messages: { en } });
const vuetify = createVuetify({ components, directives });

const kpi = (current: number, previous: number | null, buckets = [0, current]) => ({ current, previous, buckets });

const sample = (over: Partial<DashboardKpis> = {}): DashboardKpis => ({
  period: "24h",
  from: "2026-10-26T10:00:00Z",
  to: "2026-10-27T10:00:00Z",
  bucketSeconds: 3600,
  generatedAt: "2026-10-27T10:00:00Z",
  accounts: { total: 4, connected: 3, attention: 1 },
  messagesReceived: kpi(840, 790),
  messagesSent: kpi(120, 98),
  messagesFailed: kpi(2, 0),
  webhooksDelivered: kpi(900, 870),
  webhooksFailed: kpi(4, 9),
  ...over,
});

const seedMe = (role: string, instanceRole = "member", permissions: string[] = []): void => {
  const session = useSessionStore();
  session.me = {
    kind: "user",
    mfa: true,
    permissions,
    memberships: [],
    role,
    user: { id: "u1", email: "a@b.c", name: "Ada", locale: "en", instanceRole, status: "active" },
  } as never;
};

/** Runs the composable inside an i18n-aware component scope. */
const withSetup = <T>(fn: () => T): { result: T; unmount: () => void } => {
  let result!: T;
  const w = mount({ setup: () => { result = fn(); return () => null; } }, { global: { plugins: [i18n] } });
  return { result, unmount: () => w.unmount() };
};

describe("kpiHealth", () => {
  it("computes trends with polarity", () => {
    expect(computeTrend(5, null, "bad").kind).toBe("none");
    expect(computeTrend(0, 0, "bad").kind).toBe("flat");
    expect(computeTrend(3, 0, "bad")).toMatchObject({ kind: "new", color: "error" });
    expect(computeTrend(3, 0, "neutral").color).toBe("medium-emphasis");
    expect(computeTrend(150, 100, "bad")).toMatchObject({ kind: "up", pct: 50, color: "error" });
    expect(computeTrend(50, 100, "bad")).toMatchObject({ kind: "down", pct: 50, color: "success" });
    expect(computeTrend(150, 100, "neutral")).toMatchObject({ kind: "up", color: "medium-emphasis" });
    expect(computeTrend(1001, 1000, "bad").kind).toBe("flat");
  });

  it("maps thresholds to accents", () => {
    expect(failureAccent(0, 0)).toBe("secondary");
    expect(failureAccent(1, 99)).toBe("success");
    expect(failureAccent(4, 96)).toBe("warning");
    expect(failureAccent(10, 90)).toBe("error");
    expect(attentionAccent(0)).toBe("success");
    expect(attentionAccent(2)).toBe("error");
    expect(queueAccent(0, false)).toBe("success");
    expect(queueAccent(0, true)).toBe("warning");
    expect(queueAccent(500, false)).toBe("warning");
    expect(queueAccent(5000, true)).toBe("error");
    expect(latencyAccent(null, API_P95_MS)).toBe("secondary");
    expect(latencyAccent(10, MONGO_PING_MS)).toBe("success");
    expect(latencyAccent(100, MONGO_PING_MS)).toBe("warning");
    expect(latencyAccent(300, MONGO_PING_MS)).toBe("error");
    expect(latencyAccent(1, MONGO_PING_MS, true)).toBe("error");
    expect(worstAccent("success", "warning")).toBe("warning");
    expect(worstAccent("error", "warning")).toBe("error");
  });
});

describe("useDashboardKpis", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.mocked(dash.getKpis).mockResolvedValue(sample());
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("restores the saved period and builds the base cards for a viewer", async () => {
    seedMe("viewer");
    const { result, unmount } = withSetup(() => useDashboardKpis());
    expect(result.expectedCount.value).toBe(8);
    await result.init();
    expect(result.period.value).toBe("7d");
    expect(dash.getKpis).toHaveBeenCalledWith({ period: "7d" });
    const ids = result.cards.value.map((c) => c.id);
    expect(ids).toEqual([
      "accountsTotal", "accountsConnected", "accountsAttention", "messagesReceived",
      "messagesSent", "messagesFailed", "webhooksDelivered", "webhooksFailed",
    ]);
    const attention = result.cards.value.find((c) => c.id === "accountsAttention");
    expect(attention?.accent).toBe("error");
    const failed = result.cards.value.find((c) => c.id === "webhooksFailed");
    expect(failed?.href).toContain("/deliveries?state=failed%2CdeadLettered&from=");
    // The Outbox date range is createdFrom / createdTo (its from / to are envelope addresses).
    const sentHref = result.cards.value.find((c) => c.id === "messagesSent")?.href ?? "";
    const sentQuery = new URLSearchParams(sentHref.split("?")[1]);
    expect(sentHref.startsWith("/outbox?")).toBe(true);
    expect(sentQuery.get("state")).toBe("sent");
    expect(sentQuery.get("createdFrom")).toBe(sample().from);
    expect(sentQuery.get("createdTo")).toBe(sample().to);
    expect(sentQuery.has("from")).toBe(false);
    const failedOut = new URLSearchParams(
      (result.cards.value.find((c) => c.id === "messagesFailed")?.href ?? "").split("?")[1],
    );
    expect(failedOut.get("state")).toBe("failed");
    expect(failedOut.get("createdFrom")).toBe(sample().from);
    expect(failed?.accent).toBe("success"); // 4 / 904
    expect(result.historyLimited.value).toBe(false);
    unmount();
  });

  it("adds api, queue and infra cards when the server sends them", async () => {
    seedMe("owner", "owner", ["logs.read", "cluster.read"]);
    vi.mocked(dash.getKpis).mockResolvedValue(sample({
      messagesReceived: { current: 3, previous: null, buckets: [3], partial: true },
      apiCalls: {
        succeeded: kpi(100, 90), failed: kpi(6, 1), clientErrors: 5, serverErrors: 1, latencyP95Ms: 120,
      },
      queues: [
        { key: "webhooks", stream: "S1", waiting: 3, inProgress: 1, paused: false },
        { key: "submission", stream: "S2", waiting: 0, inProgress: 0, paused: true },
        { key: "events", stream: "S4", waiting: 0, inProgress: 0, paused: false },
      ],
      infra: { mongoPingMs: 0.8, busRttMs: 0.2 },
    }));
    const { result, unmount } = withSetup(() => useDashboardKpis());
    expect(result.expectedCount.value).toBe(14);
    await result.init();
    const by = (id: string) => result.cards.value.find((c) => c.id === id);
    expect(by("apiFailed")?.tooltip).toContain("5 client errors");
    expect(by("apiFailed")?.href).toContain("level=warn");
    expect(by("queue-webhooks")?.sub).toBe("1 in progress");
    expect(by("queue-submission")?.accent).toBe("warning");
    expect(by("queue-sysmail")).toBeUndefined();
    expect(by("health")?.value).toBe("0.8 ms");
    expect(by("health")?.accent).toBe("success");
    expect(result.historyLimited.value).toBe(true);
    unmount();
  });

  it("shows p95 as the health card without infra and probe errors with infra", async () => {
    seedMe("admin");
    vi.mocked(dash.getKpis).mockResolvedValueOnce(sample({
      apiCalls: { succeeded: kpi(1, 0), failed: kpi(0, 0), clientErrors: 0, serverErrors: 0, latencyP95Ms: 2500 },
    }));
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init();
    const health = result.cards.value.find((c) => c.id === "health");
    expect(health?.label).toBe("API latency (p95)");
    expect(health?.accent).toBe("error");

    vi.mocked(dash.getKpis).mockResolvedValueOnce(sample({
      infra: { mongoPingMs: 0, mongoError: "timeout", busRttMs: 0, busError: "unreachable" },
    }));
    await result.load();
    const h2 = result.cards.value.find((c) => c.id === "health");
    expect(h2?.value).toBe("Timeout");
    expect(h2?.sub).toBe("Bus Unreachable");
    expect(h2?.accent).toBe("error");
    unmount();
  });

  it("persists a period change, ignores invalid values and keeps data on a failed refetch", async () => {
    seedMe("viewer");
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init();
    result.setPeriod("bogus");
    result.setPeriod("7d"); // unchanged
    expect(dash.saveKpiSelection).not.toHaveBeenCalled();
    result.setPeriod("1h");
    expect(dash.saveKpiSelection).toHaveBeenCalledWith({ period: "1h" });
    await flushPromises();
    expect(dash.getKpis).toHaveBeenLastCalledWith({ period: "1h" });

    vi.mocked(dash.getKpis).mockRejectedValueOnce(new Error("boom"));
    await result.poll();
    expect(result.error.value).toBeNull(); // silent poll keeps the screen as it is
    expect(result.cards.value.length).toBe(8);
    vi.mocked(dash.getKpis).mockRejectedValueOnce(new Error("down"));
    await result.load();
    expect(result.error.value).toBeTruthy();
    expect(result.cards.value.length).toBe(8);
    unmount();
  });

  it("reverts the toggle when a period change fails and retries the failed period", async () => {
    seedMe("viewer");
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init(); // 7d from settings
    vi.mocked(dash.getKpis).mockRejectedValueOnce(new Error("down"));
    result.setPeriod("1h");
    await flushPromises();
    expect(result.error.value).toBeTruthy();
    expect(result.period.value).toBe("24h"); // the sample data the cards still show
    expect(result.cards.value.length).toBe(8);
    expect(result.isRefreshing.value).toBe(false);
    await result.retry();
    expect(dash.getKpis).toHaveBeenLastCalledWith({ period: "1h" });
    expect(result.error.value).toBeNull();
    unmount();
  });

  it("falls back to 24h when settings fail and reports a first-load error", async () => {
    seedMe("viewer");
    vi.mocked(dash.getSavedKpiSelection).mockRejectedValueOnce(new Error("x"));
    vi.mocked(dash.getKpis).mockRejectedValueOnce(new Error("nope"));
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init();
    expect(result.period.value).toBe("24h");
    expect(result.error.value).toBeTruthy();
    expect(result.isFirstLoad.value).toBe(false);
    expect(result.cards.value).toEqual([]);
    unmount();
  });

  it("polls every 30 s while visible and stops on dispose", async () => {
    vi.useFakeTimers();
    seedMe("viewer");
    const scope = effectScope();
    const { result, unmount } = withSetup(() => scope.run(() => useDashboardKpis())!);
    await result.init();
    const calls = vi.mocked(dash.getKpis).mock.calls.length;
    result.startPolling();
    result.startPolling(); // idempotent
    await vi.advanceTimersByTimeAsync(KPI_POLL_MS);
    expect(vi.mocked(dash.getKpis).mock.calls.length).toBe(calls + 1);
    document.dispatchEvent(new Event("visibilitychange"));
    await flushPromises();
    expect(vi.mocked(dash.getKpis).mock.calls.length).toBe(calls + 2);
    scope.stop();
    await vi.advanceTimersByTimeAsync(KPI_POLL_MS * 2);
    expect(vi.mocked(dash.getKpis).mock.calls.length).toBe(calls + 2);
    unmount();
  });
});

describe("KpiCard / KpiTrend", () => {
  it("renders label, value, slots and emits click only when selectable", async () => {
    const w = mount(KpiCard, {
      props: { label: "New emails", value: "840", accent: "info", selectable: true },
      slots: {
        icon: "<i class='ico'></i>",
        meta: "<span class='meta'>+6 %</span>",
        chart: "<div class='spark'></div>",
        footer: "<span class='foot'>f</span>",
      },
      global: { plugins: [vuetify, i18n] },
    });
    expect(w.text()).toContain("New emails");
    expect(w.text()).toContain("840");
    expect(w.find(".meta").exists()).toBe(true);
    expect(w.find(".spark").exists()).toBe(true);
    expect(w.find(".foot").exists()).toBe(true);
    await w.find(".app-card").trigger("click");
    await w.find(".app-card").trigger("keydown.enter");
    expect(w.emitted("click")?.length).toBe(2);

    const s = mount(KpiCard, { props: { label: "L" }, global: { plugins: [vuetify, i18n] } });
    await s.find(".app-card").trigger("click");
    await s.find(".app-card").trigger("keydown.enter");
    expect(s.emitted("click")).toBeUndefined();
    expect(s.find(".kpi-card__chart").exists()).toBe(false);
  });

  it("renders trend text, colour and hints", () => {
    const up = mount(KpiTrend, {
      props: { current: 150, previous: 100, polarity: "bad" },
      global: { plugins: [vuetify, i18n] },
    });
    expect(up.text()).toContain("50%");
    expect(up.html()).toContain("text-error");
    // The hint is a v-tippy tooltip; this stub exposes the directive value for the assertion.
    const tippy = { mounted: (el: HTMLElement, b: { value: unknown }) => el.setAttribute("data-tip", String(b.value)) };
    const none = mount(KpiTrend, {
      props: { current: 1, previous: null, polarity: "bad" },
      global: { plugins: [vuetify, i18n], directives: { tippy } },
    });
    expect(none.text()).toContain("No comparison");
    expect(none.attributes("data-tip")).toContain("retention");
    const fresh = mount(KpiTrend, {
      props: { current: 1, previous: 0, polarity: "neutral", partial: true },
      global: { plugins: [vuetify, i18n], directives: { tippy } },
    });
    expect(fresh.text()).toContain("New");
    expect(fresh.attributes("data-tip")).toContain("part of this period");
    const flat = mount(KpiTrend, {
      props: { current: 0, previous: 0, polarity: "neutral" },
      global: { plugins: [vuetify, i18n] },
    });
    expect(flat.text()).toContain("Steady");
    const down = mount(KpiTrend, {
      props: { current: 50, previous: 100, polarity: "bad" },
      global: { plugins: [vuetify, i18n] },
    });
    expect(down.html()).toContain("text-success");
  });
});

describe("dashboard api helpers", async () => {
  const real = await vi.importActual<typeof import("@/api/dashboard")>("@/api/dashboard");
  it("validates periods", () => {
    expect(real.isKpiPeriod("1h")).toBe(true);
    expect(real.isKpiPeriod("2h")).toBe(false);
    expect(real.isKpiPeriod(3)).toBe(false);
  });
});

vi.mock("echarts/core", () => ({ use: vi.fn() }));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));
vi.mock("echarts/charts", () => ({ LineChart: {} }));
vi.mock("echarts/components", () => ({ GridComponent: {} }));
vi.mock("vue-echarts", () => ({
  default: { name: "VChartStub", props: ["option"], template: "<div class='vchart-stub'></div>" },
}));

describe("KpiSparkline", () => {
  it(
    "draws a line with the theme colour once data exists, a baseline otherwise",
    async () => {
      const { default: KpiSparkline } = await import("@/features/home/components/KpiSparkline.vue");
      const w = mount(KpiSparkline, {
        props: { values: [0, 2, 5], color: "error" },
        global: { plugins: [vuetify, i18n] },
      });
      await flushPromises();
      const chart = w.findComponent({ name: "VChartStub" });
      expect(chart.exists()).toBe(true);
      const opt = chart.props("option") as {
        series: { data: number[]; lineStyle: { color: string } }[];
      };
      expect(opt.series[0].data).toEqual([0, 2, 5]);
      expect(opt.series[0].lineStyle.color).toMatch(/^#/);
      const empty = mount(KpiSparkline, {
        props: { values: [0, 0], color: "nope" },
        global: { plugins: [vuetify, i18n] },
      });
      await flushPromises();
      expect(empty.find(".kpi-spark__base").exists()).toBe(true);
    },
    15_000,
  );
});

describe("useDashboardKpis — custom window", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    vi.mocked(dash.getKpis).mockResolvedValue(sample());
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("waits for a window, then loads and persists it", async () => {
    seedMe("viewer");
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init();
    vi.mocked(dash.getKpis).mockClear();
    result.setPeriod("custom");
    expect(result.period.value).toBe("custom");
    expect(dash.getKpis).not.toHaveBeenCalled(); // no window picked yet
    const from = "2026-10-01T00:00:00.000Z";
    const to = "2026-10-15T23:59:59.999Z";
    result.setCustomRange(from, to);
    await flushPromises();
    expect(dash.getKpis).toHaveBeenLastCalledWith({ period: "custom", from, to });
    expect(dash.saveKpiSelection).toHaveBeenLastCalledWith({ period: "custom", from, to });
    unmount();
  });

  it("ignores a window longer than the maximum or reversed", async () => {
    seedMe("viewer");
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init();
    vi.mocked(dash.getKpis).mockClear();
    result.setCustomRange("2026-01-01T00:00:00.000Z", "2026-06-01T00:00:00.000Z"); // > 92 days
    result.setCustomRange("2026-10-15T00:00:00.000Z", "2026-10-01T00:00:00.000Z"); // reversed
    await flushPromises();
    expect(dash.getKpis).not.toHaveBeenCalled();
    unmount();
  });

  it("restores a saved window and does not poll a window that ended in the past", async () => {
    seedMe("viewer");
    const from = "2026-09-01T00:00:00.000Z";
    const to = "2026-09-10T23:59:59.999Z";
    vi.mocked(dash.getSavedKpiSelection).mockResolvedValueOnce({ period: "custom", from, to });
    const { result, unmount } = withSetup(() => useDashboardKpis());
    await result.init();
    expect(result.period.value).toBe("custom");
    expect(dash.getKpis).toHaveBeenLastCalledWith({ period: "custom", from, to });
    vi.mocked(dash.getKpis).mockClear();
    await result.poll();
    expect(dash.getKpis).not.toHaveBeenCalled();
    unmount();
  });
});
