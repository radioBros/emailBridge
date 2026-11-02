import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ServerTableSortItem } from "@/components/tables/types";
import {
  bulkReplayDeliveries,
  listDeliveries,
  replayDelivery,
  type BulkReplayBody,
  type Delivery,
} from "@/api/deliveries";
import { listWebhookEndpoints } from "@/api/webhooks";
import { searchAccounts, type AccountView } from "@/api/outbox";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ensureLabels } from "@/features/deliveries/helpers/labelCache";
import { FAILED_QUICK, toApiDate } from "@/features/deliveries/helpers/states";
import { jobProgressText, pollJob } from "@/features/deliveries/helpers/jobPoll";
import { useI18n } from "vue-i18n";

const SORT_KEYS = new Set(["createdAt", "state", "lastAttemptAt", "id"]);

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first || !SORT_KEYS.has(first.key)) return "-createdAt";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

export const useDeliveriesList = () => {
  const route = useRoute();
  const router = useRouter();
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();

  const items = ref<Delivery[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const search = ref(readQuery(route.query.q));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const filterAccountId = ref(readQuery(route.query.accountId));
  const filterEndpointId = ref(readQuery(route.query.endpointId));
  const filterEventType = ref(readQuery(route.query.eventType));
  const filterState = ref(readQuery(route.query.state));
  const filterFrom = ref(readQuery(route.query.from));
  const filterTo = ref(readQuery(route.query.to));
  const failedQuick = ref(filterState.value === FAILED_QUICK);

  const selected = ref<string[]>([]);
  const replaying = ref(false);
  const jobBusy = ref(false);

  const accountOptions = ref<AccountView[]>([]);
  const endpointOptions = ref<{ id: string; name: string }[]>([]);
  const accountsLoading = ref(false);
  const endpointsLoading = ref(false);

  const hasActiveFilters = computed(
    () =>
      Boolean(filterAccountId.value) ||
      Boolean(filterEndpointId.value) ||
      Boolean(filterEventType.value) ||
      Boolean(filterState.value) ||
      Boolean(filterFrom.value) ||
      Boolean(filterTo.value) ||
      Boolean(search.value),
  );

  const activeFilterCount = computed(() => {
    let n = 0;
    if (filterAccountId.value) n += 1;
    if (filterEndpointId.value) n += 1;
    if (filterEventType.value) n += 1;
    if (filterState.value) n += 1;
    if (filterFrom.value || filterTo.value) n += 1;
    if (search.value) n += 1;
    return n;
  });

  const empty = computed(
    () =>
      !isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      !hasActiveFilters.value,
  );

  const noResults = computed(
    () =>
      !isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      hasActiveFilters.value,
  );

  const syncUrl = (): void => {
    const query: Record<string, string> = {};
    if (page.value > 1) query.page = String(page.value);
    if (ipp.value !== 25) query.limit = String(ipp.value);
    if (filterAccountId.value) query.accountId = filterAccountId.value;
    if (filterEndpointId.value) query.endpointId = filterEndpointId.value;
    if (filterEventType.value) query.eventType = filterEventType.value;
    if (filterState.value) query.state = filterState.value;
    if (filterFrom.value) query.from = filterFrom.value;
    if (filterTo.value) query.to = filterTo.value;
    if (search.value) query.q = search.value;
    void router.replace({ query });
  };

  const currentFilters = (): BulkReplayBody => ({
    endpointId: filterEndpointId.value || undefined,
    accountId: filterAccountId.value || undefined,
    eventType: filterEventType.value || undefined,
    state: filterState.value || undefined,
    from: toApiDate(filterFrom.value),
    to: toApiDate(filterTo.value),
  });

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listDeliveries({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        q: search.value || undefined,
        accountId: filterAccountId.value || undefined,
        endpointId: filterEndpointId.value || undefined,
        eventType: filterEventType.value || undefined,
        state: filterState.value || undefined,
        from: toApiDate(filterFrom.value),
        to: toApiDate(filterTo.value),
      });
      items.value = res.data ?? [];
      total.value = res.total ?? items.value.length;
      selected.value = selected.value.filter((id) => items.value.some((r) => r.id === id));
      await ensureLabels({
        accountIds: items.value.map((r) => r.accountId ?? ""),
        endpointIds: items.value.map((r) => r.endpointId),
        routeIds: items.value.map((r) => r.routeId ?? ""),
      });
      finish(true);
      syncUrl();
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const loadAccounts = async (q = ""): Promise<void> => {
    accountsLoading.value = true;
    try {
      accountOptions.value = await searchAccounts(q);
    } catch (err) {
      accountOptions.value = [];
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
    } finally {
      accountsLoading.value = false;
    }
  };

  const loadEndpoints = async (q = ""): Promise<void> => {
    endpointsLoading.value = true;
    try {
      const res = await listWebhookEndpoints({ page: 1, limit: 50, q: q || undefined });
      endpointOptions.value = (res.data ?? []).map((e) => ({ id: e.id, name: e.name }));
    } catch (err) {
      endpointOptions.value = [];
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
    } finally {
      endpointsLoading.value = false;
    }
  };

  const clearFilters = (): void => {
    filterAccountId.value = "";
    filterEndpointId.value = "";
    filterEventType.value = "";
    filterState.value = "";
    filterFrom.value = "";
    filterTo.value = "";
    search.value = "";
    failedQuick.value = false;
    page.value = 1;
    void load();
  };

  const setFailedQuick = (on: boolean): void => {
    failedQuick.value = on;
    filterState.value = on ? FAILED_QUICK : "";
    page.value = 1;
    void load();
  };

  const trackJob = async (jobId: string): Promise<void> => {
    jobBusy.value = true;
    const ac = new AbortController();
    try {
      const job = await pollJob(
        jobId,
        (j) => {
          notify.push({
            message: t("deliveries.job_progress", { progress: jobProgressText(j) }),
            color: "info",
          });
        },
        ac.signal,
      );
      const color =
        job.state === "succeeded" ? "success" : job.state === "cancelled" ? "warning" : "error";
      notify.push({ message: t(`deliveries.job_${job.state}`), color });
      void load();
    } catch (err) {
      if ((err as Error).name !== "AbortError") {
        notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
      }
    } finally {
      jobBusy.value = false;
    }
  };

  const replayOne = async (id: string): Promise<boolean> => {
    replaying.value = true;
    try {
      await replayDelivery(id);
      notify.push({ message: t("deliveries.replayed"), color: "success" });
      void load();
      return true;
    } catch (err) {
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
      return false;
    } finally {
      replaying.value = false;
    }
  };

  const replaySelected = async (): Promise<boolean> => {
    if (!selected.value.length) return false;
    replaying.value = true;
    try {
      const res = await bulkReplayDeliveries({ ids: [...selected.value] });
      const job = res.job;
      selected.value = [];
      void trackJob(job.id);
      return true;
    } catch (err) {
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
      return false;
    } finally {
      replaying.value = false;
    }
  };

  const replayMatching = async (includeReplayed = false): Promise<boolean> => {
    const body = currentFilters();
    if (
      !body.endpointId &&
      !body.routeId &&
      !body.eventType &&
      !body.accountId &&
      !body.state &&
      !body.from &&
      !body.to
    ) {
      notify.push({ message: t("deliveries.replay_need_filter"), color: "warning" });
      return false;
    }
    if (includeReplayed) body.includeReplayed = true;
    replaying.value = true;
    try {
      const res = await bulkReplayDeliveries(body);
      void trackJob(res.job.id);
      return true;
    } catch (err) {
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
      return false;
    } finally {
      replaying.value = false;
    }
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
    filterAccountId,
    filterEndpointId,
    filterEventType,
    filterState,
    filterFrom,
    filterTo,
    failedQuick,
    selected,
    replaying,
    jobBusy,
    accountOptions,
    endpointOptions,
    accountsLoading,
    endpointsLoading,
    loadError,
    empty,
    noResults,
    activeFilterCount,
    isFirstLoad,
    isRefreshing,
    load,
    loadAccounts,
    loadEndpoints,
    clearFilters,
    setFailedQuick,
    replayOne,
    replaySelected,
    replayMatching,
    currentFilters,
  };
};
