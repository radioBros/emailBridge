import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ServerTableSortItem } from "@/components/tables/types";
import { listEvents, type EbEvent } from "@/api/events";
import { searchAccounts, type AccountView } from "@/api/outbox";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ensureAccountLabels } from "@/features/events/helpers/accountCache";
import { toApiDate } from "@/features/events/helpers/types";
import { useEventStream } from "@/features/events/composables/useEventStream";

const SORT_KEYS = new Set(["time", "id"]);

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first || !SORT_KEYS.has(first.key)) return "-time";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

export const useEventsList = () => {
  const route = useRoute();
  const router = useRouter();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();

  const items = ref<EbEvent[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const sortBy = ref<ServerTableSortItem[]>([{ key: "time", order: "desc" }]);
  const filterType = ref(readQuery(route.query.type));
  const filterAccountId = ref(readQuery(route.query.accountId));
  const filterFrom = ref(readQuery(route.query.from));
  const filterTo = ref(readQuery(route.query.to));
  const highlighted = ref<Set<string>>(new Set());

  const accountOptions = ref<AccountView[]>([]);
  const accountsLoading = ref(false);

  const stream = useEventStream(items);

  const hasActiveFilters = computed(
    () =>
      Boolean(filterType.value) ||
      Boolean(filterAccountId.value) ||
      Boolean(filterFrom.value) ||
      Boolean(filterTo.value),
  );

  const activeFilterCount = computed(() => {
    let n = 0;
    if (filterType.value) n += 1;
    if (filterAccountId.value) n += 1;
    if (filterFrom.value || filterTo.value) n += 1;
    return n;
  });

  const empty = computed(
    () =>
      !isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      !hasActiveFilters.value &&
      !stream.live.value,
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
    if (filterType.value) query.type = filterType.value;
    if (filterAccountId.value) query.accountId = filterAccountId.value;
    if (filterFrom.value) query.from = filterFrom.value;
    if (filterTo.value) query.to = filterTo.value;
    void router.replace({ query });
  };

  const load = async (): Promise<void> => {
    if (stream.live.value) return;
    start();
    loadError.value = null;
    try {
      const res = await listEvents({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        type: filterType.value || undefined,
        accountId: filterAccountId.value || undefined,
        from: toApiDate(filterFrom.value),
        to: toApiDate(filterTo.value),
      });
      items.value = res.data ?? [];
      total.value = res.total ?? items.value.length;
      await ensureAccountLabels(items.value.map((r) => r.ebaccount ?? ""));
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

  const clearFilters = (): void => {
    filterType.value = "";
    filterAccountId.value = "";
    filterFrom.value = "";
    filterTo.value = "";
    page.value = 1;
    void load();
  };

  const toggleLive = async (): Promise<void> => {
    if (stream.live.value || stream.reconnecting.value) {
      stream.stop();
      void load();
      return;
    }
    stream.error.value = null;
    const before = new Set(items.value.map((r) => r.id));
    await stream.start({
      type: filterType.value || undefined,
      accountId: filterAccountId.value || undefined,
    });
    if (stream.error.value) {
      notify.push({ message: stream.error.value, color: "error" });
      return;
    }
    const neu = new Set<string>();
    for (const row of items.value) {
      if (!before.has(row.id)) neu.add(row.id);
    }
    highlighted.value = neu;
    window.setTimeout(() => {
      highlighted.value = new Set();
    }, 2500);
  };

  watch([page, ipp, sortBy], () => {
    if (!stream.live.value) void load();
  });

  return {
    items,
    total,
    page,
    ipp,
    sortBy,
    filterType,
    filterAccountId,
    filterFrom,
    filterTo,
    accountOptions,
    accountsLoading,
    loadError,
    empty,
    noResults,
    activeFilterCount,
    isFirstLoad,
    isRefreshing,
    highlighted,
    stream,
    load,
    loadAccounts,
    clearFilters,
    toggleLive,
  };
};
