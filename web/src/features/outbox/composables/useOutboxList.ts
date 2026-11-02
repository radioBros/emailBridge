import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ServerTableSortItem } from "@/components/tables/types";
import {
  listOutboundMessages,
  searchAccounts,
  type AccountView,
  type SubmitView,
} from "@/api/outbox";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { toApiDate } from "@/features/outbox/helpers/outboundState";
import {
  ensureAccountLabels,
  rememberAccounts,
} from "@/features/outbox/helpers/accountCache";

const SORT_KEYS = new Set(["createdAt", "sendAt", "state", "id"]);

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first || !SORT_KEYS.has(first.key)) return "-createdAt";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

/** Outbox table: page/sort/filters mirrored in the URL. */
export const useOutboxList = () => {
  const route = useRoute();
  const router = useRouter();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();

  const items = ref<SubmitView[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const search = ref(readQuery(route.query.q));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const filterAccountId = ref(readQuery(route.query.accountId));
  const filterState = ref(readQuery(route.query.state));
  // Creation-date range (URL + API: createdFrom / createdTo; `from` / `to` are envelope addresses).
  const filterFrom = ref(readQuery(route.query.createdFrom));
  const filterTo = ref(readQuery(route.query.createdTo));

  const accountOptions = ref<AccountView[]>([]);
  const accountsLoading = ref(false);

  const hasActiveFilters = computed(
    () =>
      Boolean(filterAccountId.value) ||
      Boolean(filterState.value) ||
      Boolean(filterFrom.value) ||
      Boolean(filterTo.value) ||
      Boolean(search.value),
  );

  const activeFilterCount = computed(() => {
    let n = 0;
    if (filterAccountId.value) n += 1;
    if (filterState.value) n += 1;
    if (filterFrom.value || filterTo.value) n += 1;
    if (search.value) n += 1;
    return n;
  });

  /** True empty inbox — no filters. Filtered zero rows keep the table. */
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
    if (filterState.value) query.state = filterState.value;
    if (filterFrom.value) query.createdFrom = filterFrom.value;
    if (filterTo.value) query.createdTo = filterTo.value;
    if (search.value) query.q = search.value;
    void router.replace({ query });
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listOutboundMessages({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        accountId: filterAccountId.value || undefined,
        state: filterState.value || undefined,
        createdFrom: toApiDate(filterFrom.value),
        createdTo: toApiDate(filterTo.value),
        messageId: search.value || undefined,
      });
      items.value = res.data ?? [];
      total.value = res.total ?? items.value.length;
      await ensureAccountLabels(items.value.map((r) => r.accountId));
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
      const rows = await searchAccounts(q);
      accountOptions.value = rows;
      rememberAccounts(rows);
    } catch (err) {
      accountOptions.value = [];
      notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
    } finally {
      accountsLoading.value = false;
    }
  };

  const clearFilters = (): void => {
    filterAccountId.value = "";
    filterState.value = "";
    filterFrom.value = "";
    filterTo.value = "";
    search.value = "";
    page.value = 1;
  };

  watch([page, ipp, sortBy, filterAccountId, filterState, filterFrom, filterTo], () => {
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
    filterState,
    filterFrom,
    filterTo,
    accountOptions,
    accountsLoading,
    loadError,
    empty,
    noResults,
    hasActiveFilters,
    activeFilterCount,
    isFirstLoad,
    isRefreshing,
    isLoading,
    load,
    loadAccounts,
    clearFilters,
  };
};
