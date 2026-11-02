import { computed, ref, watch, type Ref } from "vue";
import { useI18n } from "vue-i18n";
import type { ServerTableSortItem } from "@/components/tables/types";
import { cancelQueueItems, listQueueItems, type QueueItem, type QueueScope } from "@/api/queues";
import type { Job } from "@/api/deliveries";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ensureLabels } from "@/features/deliveries/helpers/labelCache";
import { pollJob } from "@/features/deliveries/helpers/jobPoll";
import { filterBody, isEmptyFilter, sortParam, type QueueDef } from "@/features/queues/helpers/queues";

export interface JobProgress {
  id: string;
  state: string;
  done: number;
  total: number;
}

/**
 * One queue tab: server-side list (page / limit / sort / search / filters), selection and every cancel
 * path (row, selected, all matching, entire queue). Failures always reach the user as a notification.
 */
export const useQueueItems = (def: QueueDef, scope: Ref<QueueScope>, onChanged: () => void = () => undefined) => {
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isLoading, isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const items = ref<QueueItem[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(1);
  const ipp = ref(25);
  const search = ref("");
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "asc" }]);
  const filterState = ref("");
  const filterAccountId = ref("");
  const filterEndpointId = ref("");
  const filterFrom = ref("");
  const filterTo = ref("");
  const selected = ref<string[]>([]);
  const cancelling = ref(false);
  const cancellingId = ref<string | null>(null);
  const job = ref<JobProgress | null>(null);

  const filters = computed(() =>
    filterBody({
      state: filterState.value,
      accountId: filterAccountId.value,
      endpointId: filterEndpointId.value,
      from: filterFrom.value,
      to: filterTo.value,
      q: search.value,
    }),
  );

  const hasFilters = computed(() => !isEmptyFilter(filters.value));

  const activeFilterCount = computed(
    () =>
      [filterState.value, filterAccountId.value, filterEndpointId.value, filterFrom.value || filterTo.value].filter(
        Boolean,
      ).length,
  );

  const empty = computed(() => !isLoading.value && !loadError.value && items.value.length === 0 && !hasFilters.value);
  const noResults = computed(
    () => !isLoading.value && !loadError.value && items.value.length === 0 && hasFilters.value,
  );

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listQueueItems(def.name, {
        ...filters.value,
        scope: scope.value,
        page: page.value,
        limit: ipp.value,
        sort: sortParam(sortBy.value),
      });
      items.value = res.data ?? [];
      total.value = res.total ?? items.value.length;
      selected.value = selected.value.filter((id) => items.value.some((r) => r.id === id && r.cancellable));
      await ensureLabels({
        accountIds: items.value.map((r) => r.accountId ?? ""),
        endpointIds: items.value.map((r) => r.endpointId ?? ""),
        routeIds: [],
      });
      finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const fail = (err: unknown): void => {
    notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
  };

  const done = (): void => {
    onChanged();
    void load();
  };

  const trackJob = async (j: Job, matched: number): Promise<void> => {
    job.value = { id: j.id, state: j.state, done: 0, total: matched };
    try {
      const final = await pollJob(j.id, (cur) => {
        job.value = {
          id: cur.id,
          state: cur.state,
          done: cur.progress?.done ?? 0,
          total: cur.progress?.total || matched,
        };
      });
      const color = final.state === "succeeded" ? "success" : final.state === "cancelled" ? "warning" : "error";
      const result = (final.result ?? {}) as { cancelled?: number };
      notify.push({
        message: t(`queues.job_${final.state}`, { n: result.cancelled ?? job.value?.done ?? 0 }),
        color,
      });
    } catch (err) {
      fail(err);
    } finally {
      job.value = null;
      done();
    }
  };

  const cancelOne = async (id: string): Promise<boolean> => {
    cancellingId.value = id;
    try {
      await cancelQueueItems(def.name, { ids: [id], scope: scope.value });
      notify.push({ message: t("queues.cancelled_one"), color: "success" });
      selected.value = selected.value.filter((x) => x !== id);
      done();
      return true;
    } catch (err) {
      fail(err);
      void load();
      return false;
    } finally {
      cancellingId.value = null;
    }
  };

  const cancelSelected = async (): Promise<boolean> => {
    if (!selected.value.length) return false;
    cancelling.value = true;
    try {
      const res = await cancelQueueItems(def.name, { ids: [...selected.value], scope: scope.value });
      const skipped = res.skipped?.length ?? 0;
      notify.push({
        message: skipped
          ? t("queues.cancelled_some", { n: res.cancelled ?? 0, skipped })
          : t("queues.cancelled_n", { n: res.cancelled ?? 0 }),
        color: skipped ? "warning" : "success",
      });
      selected.value = [];
      done();
      return true;
    } catch (err) {
      fail(err);
      return false;
    } finally {
      cancelling.value = false;
    }
  };

  const startBulk = async (body: Parameters<typeof cancelQueueItems>[1]): Promise<boolean> => {
    cancelling.value = true;
    try {
      const res = await cancelQueueItems(def.name, { ...body, scope: scope.value });
      if (res.job) {
        void trackJob(res.job, res.matched ?? 0);
      } else {
        notify.push({ message: t("queues.nothing_matched"), color: "info" });
        done();
      }
      return true;
    } catch (err) {
      fail(err);
      return false;
    } finally {
      cancelling.value = false;
    }
  };

  /** Cancel every cancellable record matching the filters (needs at least one filter). */
  const cancelMatching = async (): Promise<boolean> => {
    if (!hasFilters.value) {
      notify.push({ message: t("queues.need_filter"), color: "warning" });
      return false;
    }
    return startBulk({ filter: filters.value });
  };

  /** Cancel the whole queue in the scope; `confirm` is the typed queue name. */
  const cancelAll = (confirm: string): Promise<boolean> => startBulk({ all: true, confirm });

  const toggleSelect = (id: string, on: boolean): void => {
    selected.value = on ? [...new Set([...selected.value, id])] : selected.value.filter((x) => x !== id);
  };

  const selectablePage = computed(() => items.value.filter((r) => r.cancellable).map((r) => r.id));

  const allSelectedOnPage = computed({
    get: () => selectablePage.value.length > 0 && selectablePage.value.every((id) => selected.value.includes(id)),
    set: (on: boolean) => {
      const pageIds = new Set(selectablePage.value);
      selected.value = on
        ? [...new Set([...selected.value, ...pageIds])]
        : selected.value.filter((id) => !pageIds.has(id));
    },
  });

  const clearFilters = (): void => {
    filterState.value = "";
    filterAccountId.value = "";
    filterEndpointId.value = "";
    filterFrom.value = "";
    filterTo.value = "";
    search.value = "";
    page.value = 1;
    void load();
  };

  const onFilterChange = (): void => {
    page.value = 1;
    void load();
  };

  watch([page, ipp, sortBy], () => {
    void load();
  });
  watch(search, () => {
    page.value = 1;
    void load();
  });

  return {
    items,
    total,
    page,
    ipp,
    search,
    sortBy,
    filterState,
    filterAccountId,
    filterEndpointId,
    filterFrom,
    filterTo,
    filters,
    hasFilters,
    activeFilterCount,
    selected,
    allSelectedOnPage,
    cancelling,
    cancellingId,
    job,
    loadError,
    empty,
    noResults,
    isFirstLoad,
    isRefreshing,
    load,
    cancelOne,
    cancelSelected,
    cancelMatching,
    cancelAll,
    toggleSelect,
    clearFilters,
    onFilterChange,
  };
};
