import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { ref } from "vue";

vi.mock("vue-i18n", () => ({
  useI18n: () => ({
    t: (k: string, p?: Record<string, unknown>) => (p ? `${k}:${JSON.stringify(p)}` : k),
    te: () => true,
  }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({ messageOf: (err: unknown) => `problem:${String((err as { code?: string })?.code ?? err)}` }),
}));

const listQueueItems = vi.fn();
const cancelQueueItems = vi.fn();
const listQueueSummaries = vi.fn();
vi.mock("@/api/queues", () => ({
  listQueueItems: (...a: unknown[]) => listQueueItems(...a),
  cancelQueueItems: (...a: unknown[]) => cancelQueueItems(...a),
  listQueueSummaries: (...a: unknown[]) => listQueueSummaries(...a),
}));

vi.mock("@/features/deliveries/helpers/labelCache", () => ({
  ensureLabels: vi.fn(async () => undefined),
  accountLabelOf: (id: string) => id,
  endpointLabelOf: (id: string) => id,
}));

const pollJob = vi.fn();
vi.mock("@/features/deliveries/helpers/jobPoll", () => ({
  pollJob: (...a: unknown[]) => pollJob(...a),
}));

import {
  ageOf,
  backlogOf,
  canCancelIn,
  filterBody,
  isInFlight,
  QUEUE_DEFS,
  queueDefOf,
  queueStateSpec,
  sortParam,
  summaryOf,
  toApiDate,
  visibleQueues,
} from "@/features/queues/helpers/queues";
import { useQueueItems } from "@/features/queues/composables/useQueueItems";
import { useQueueSummaries } from "@/features/queues/composables/useQueueSummaries";
import { useNotifyStore } from "@/stores/notify";

const sending = QUEUE_DEFS[0]!;

const page = (rows: Array<Record<string, unknown>>) => ({ data: rows, total: rows.length, totalCapped: false, page: 1, limit: 25 });

const row = (id: string, state = "queued", cancellable = true) => ({
  id,
  queue: "EMAILBRIDGE_SUBMIT",
  kind: "outboundMessage",
  state,
  cancellable,
  attempts: 0,
  createdAt: "2026-10-27T09:00:00Z",
});

describe("queues helpers", () => {
  it("defines the cancellable queues and hides system mail from non-owners", () => {
    expect(QUEUE_DEFS.map((d) => d.name)).toEqual(["EMAILBRIDGE_SUBMIT", "EMAILBRIDGE_DELIVERY", "EMAILBRIDGE_SYSMAIL"]);
    expect(visibleQueues(false).map((d) => d.key)).toEqual(["sending", "webhooks"]);
    expect(visibleQueues(true)).toHaveLength(3);
    expect(queueDefOf("EMAILBRIDGE_EVENTS")).toBeUndefined();
  });

  it("decides who can cancel", () => {
    const sys = queueDefOf("EMAILBRIDGE_SYSMAIL")!;
    expect(canCancelIn(sending, ["read", "outbound.write"], false)).toBe(true);
    expect(canCancelIn(sending, ["read"], false)).toBe(false);
    expect(canCancelIn(sys, ["outbound.write"], false)).toBe(false);
    expect(canCancelIn(sys, [], true)).toBe(true);
  });

  it("maps states and in-flight", () => {
    expect(isInFlight("sending")).toBe(true);
    expect(isInFlight("delivering")).toBe(true);
    expect(isInFlight("retrying")).toBe(false);
    expect(queueStateSpec("deadLettered").color).toBe("error");
    expect(queueStateSpec("retrying").color).toBe("warning");
    for (const s of ["scheduled", "queued", "pending", "sending", "delivering", "bogus"]) {
      expect(queueStateSpec(s).icon).toBeTruthy();
    }
  });

  it("computes backlog and age", () => {
    expect(backlogOf(undefined)).toBe(0);
    expect(backlogOf({ waiting: 2, scheduled: 3, retrying: 1, inFlight: 9, deadLettered: 4 })).toBe(10);
    const now = Date.parse("2026-10-27T12:00:00Z");
    expect(ageOf(undefined, now)).toBeNull();
    expect(ageOf("nope", now)).toBeNull();
    expect(ageOf("2026-10-27T11:59:50Z", now)).toEqual({ unit: "minutes", value: 1 });
    expect(ageOf("2026-10-27T09:00:00Z", now)).toEqual({ unit: "hours", value: 3 });
    expect(ageOf("2026-10-24T12:00:00Z", now)).toEqual({ unit: "days", value: 3 });
  });

  it("builds the cancel filter and sort", () => {
    expect(filterBody({ state: "", accountId: "", endpointId: "", from: "", to: "", q: "  " })).toEqual({});
    const f = filterBody({ state: "retrying", accountId: "acc_1", endpointId: "whe_1", from: "2026-10-01", to: "2026-10-02", q: " anna " });
    expect(f).toEqual({
      state: "retrying",
      accountId: "acc_1",
      endpointId: "whe_1",
      createdFrom: "2026-10-01T00:00:00.000Z",
      createdTo: "2026-10-03T00:00:00.000Z",
      q: "anna",
    });
    expect(toApiDate("garbage")).toBeUndefined();
    expect(sortParam([])).toBe("createdAt");
    expect(sortParam([{ key: "nextAttemptAt", order: "desc" }])).toBe("-nextAttemptAt");
    expect(sortParam([{ key: "subject", order: "asc" }])).toBe("createdAt");
    expect(summaryOf([{ name: "A", kind: "events", cancellable: false, paused: false }], "A")?.name).toBe("A");
  });
});

describe("useQueueItems", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    listQueueItems.mockReset();
    cancelQueueItems.mockReset();
    pollJob.mockReset();
  });

  it("loads a page, keeps only cancellable selections and selects the page", async () => {
    listQueueItems.mockResolvedValue(page([row("out_1"), row("out_2", "sending", false)]));
    const q = useQueueItems(sending, ref("organization"));
    await q.load();
    expect(listQueueItems).toHaveBeenCalledWith("EMAILBRIDGE_SUBMIT", expect.objectContaining({ scope: "organization", page: 1, limit: 25, sort: "createdAt" }));
    expect(q.items.value).toHaveLength(2);
    expect(q.isFirstLoad.value).toBe(false);
    q.allSelectedOnPage.value = true;
    expect(q.selected.value).toEqual(["out_1"]);
    expect(q.allSelectedOnPage.value).toBe(true);
    q.toggleSelect("out_1", false);
    expect(q.selected.value).toEqual([]);
    q.allSelectedOnPage.value = false;
    expect(q.empty.value).toBe(false);
  });

  it("shows the load error and the empty / no-results states", async () => {
    listQueueItems.mockRejectedValueOnce({ code: "forbidden" });
    const q = useQueueItems(sending, ref("organization"));
    await q.load();
    expect(q.loadError.value).toBe("problem:forbidden");
    listQueueItems.mockResolvedValue(page([]));
    await q.load();
    expect(q.empty.value).toBe(true);
    q.filterState.value = "retrying";
    await q.load();
    expect(q.noResults.value).toBe(true);
    expect(q.activeFilterCount.value).toBe(1);
    q.clearFilters();
    expect(q.filterState.value).toBe("");
  });

  it("cancels one row and reports failures", async () => {
    listQueueItems.mockResolvedValue(page([row("out_1")]));
    const changed = vi.fn();
    const q = useQueueItems(sending, ref("all"), changed);
    const notify = useNotifyStore();
    const push = vi.spyOn(notify, "push");
    cancelQueueItems.mockResolvedValueOnce({ cancelled: 1 });
    expect(await q.cancelOne("out_1")).toBe(true);
    expect(cancelQueueItems).toHaveBeenCalledWith("EMAILBRIDGE_SUBMIT", { ids: ["out_1"], scope: "all" });
    expect(changed).toHaveBeenCalled();
    cancelQueueItems.mockRejectedValueOnce({ code: "queue_item_not_cancellable" });
    expect(await q.cancelOne("out_1")).toBe(false);
    expect(push).toHaveBeenLastCalledWith({ message: "problem:queue_item_not_cancellable", color: "error" });
  });

  it("cancels the selection and warns about skipped items", async () => {
    listQueueItems.mockResolvedValue(page([row("out_1"), row("out_2")]));
    const q = useQueueItems(sending, ref("organization"));
    const push = vi.spyOn(useNotifyStore(), "push");
    expect(await q.cancelSelected()).toBe(false);
    q.selected.value = ["out_1", "out_2"];
    cancelQueueItems.mockResolvedValueOnce({ cancelled: 1, skipped: [{ id: "out_2", reason: "inFlight" }] });
    expect(await q.cancelSelected()).toBe(true);
    expect(push).toHaveBeenCalledWith(expect.objectContaining({ color: "warning" }));
    expect(q.selected.value).toEqual([]);
    q.selected.value = ["out_1"];
    cancelQueueItems.mockResolvedValueOnce({ cancelled: 1 });
    await q.cancelSelected();
    expect(push).toHaveBeenLastCalledWith(expect.objectContaining({ color: "success" }));
    q.selected.value = ["out_1"];
    cancelQueueItems.mockRejectedValueOnce({ code: "forbidden" });
    expect(await q.cancelSelected()).toBe(false);
  });

  it("needs a filter for cancel-matching, tracks the job and handles nothing matched", async () => {
    listQueueItems.mockResolvedValue(page([row("out_1")]));
    const q = useQueueItems(sending, ref("organization"));
    const push = vi.spyOn(useNotifyStore(), "push");
    expect(await q.cancelMatching()).toBe(false);
    expect(push).toHaveBeenLastCalledWith({ message: "queues.need_filter", color: "warning" });

    q.filterState.value = "retrying";
    let resolveJob: (v: unknown) => void = () => undefined;
    pollJob.mockImplementation((_id: string, onUpdate: (j: unknown) => void) => {
      onUpdate({ id: "job_1", state: "running", progress: { done: 2, total: 5 } });
      return new Promise((r) => {
        resolveJob = r;
      });
    });
    cancelQueueItems.mockResolvedValueOnce({ job: { id: "job_1", state: "queued" }, matched: 5 });
    expect(await q.cancelMatching()).toBe(true);
    expect(cancelQueueItems).toHaveBeenLastCalledWith("EMAILBRIDGE_SUBMIT", { filter: { state: "retrying" }, scope: "organization" });
    expect(q.job.value).toEqual({ id: "job_1", state: "running", done: 2, total: 5 });
    resolveJob({ id: "job_1", state: "succeeded", result: { cancelled: 5 } });
    await vi.waitFor(() => expect(q.job.value).toBeNull());
    expect(push).toHaveBeenCalledWith({ message: 'queues.job_succeeded:{"n":5}', color: "success" });

    cancelQueueItems.mockResolvedValueOnce({ matched: 0, cancelled: 0 });
    expect(await q.cancelAll("EMAILBRIDGE_SUBMIT")).toBe(true);
    expect(cancelQueueItems).toHaveBeenLastCalledWith("EMAILBRIDGE_SUBMIT", { all: true, confirm: "EMAILBRIDGE_SUBMIT", scope: "organization" });
    expect(push).toHaveBeenLastCalledWith({ message: "queues.nothing_matched", color: "info" });

    cancelQueueItems.mockRejectedValueOnce({ code: "queue_confirmation_required" });
    expect(await q.cancelAll("x")).toBe(false);

    pollJob.mockRejectedValueOnce({ code: "internal_error" });
    cancelQueueItems.mockResolvedValueOnce({ job: { id: "job_2", state: "queued" }, matched: 1 });
    await q.cancelAll("EMAILBRIDGE_SUBMIT");
    await vi.waitFor(() => expect(push).toHaveBeenLastCalledWith({ message: "problem:internal_error", color: "error" }));
  });
});

describe("useQueueSummaries", () => {
  it("loads with the scope and reports errors", async () => {
    listQueueSummaries.mockResolvedValueOnce([{ name: "EMAILBRIDGE_SUBMIT", kind: "sending", cancellable: true, paused: false }]);
    const scope = ref<"organization" | "all">("all");
    const s = useQueueSummaries(scope);
    await s.load();
    expect(listQueueSummaries).toHaveBeenCalledWith("all");
    expect(s.summaries.value).toHaveLength(1);
    listQueueSummaries.mockRejectedValueOnce({ code: "forbidden" });
    await s.load();
    expect(s.error.value).toBe("problem:forbidden");
    expect(s.isRefreshing.value).toBe(false);
  });
});
