import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ServerTableSortItem } from "@/components/tables/types";
import {
  listSandboxMessages,
  searchAccounts,
  type SandboxView,
} from "@/api/sandbox";
import type { AccountView } from "@/api/outbox";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { toApiDate } from "@/features/sandbox/helpers/format";
import {
  ensureAccountLabels,
  rememberAccounts,
} from "@/features/sandbox/helpers/accountCache";

const SORT_KEYS = new Set(["capturedAt", "id"]);

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first || !SORT_KEYS.has(first.key)) return "-capturedAt";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

/** Sandbox table: page/sort/filters mirrored in the URL. */
export const useSandboxList = () => {
  const route = useRoute();
  const router = useRouter();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();

  const items = ref<SandboxView[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const search = ref(readQuery(route.query.q));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "capturedAt", order: "desc" }]);
  const filterAccountId = ref(readQuery(route.query.accountId));
  const filterFrom = ref(readQuery(route.query.from));
  const filterTo = ref(readQuery(route.query.to));

  const accountOptions = ref<AccountView[]>([]);
  const accountsLoading = ref(false);

  const hasActiveFilters = computed(
    () =>
      Boolean(filterAccountId.value) ||
      Boolean(filterFrom.value) ||
      Boolean(filterTo.value) ||
      Boolean(search.value),
  );

  const activeFilterCount = computed(() => {
    let n = 0;
    if (filterAccountId.value) n += 1;
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
    if (filterFrom.value) query.from = filterFrom.value;
    if (filterTo.value) query.to = filterTo.value;
    if (search.value) query.q = search.value;
    void router.replace({ query });
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listSandboxMessages({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        accountId: filterAccountId.value || undefined,
        from: toApiDate(filterFrom.value),
        to: toApiDate(filterTo.value),
        q: search.value || undefined,
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

  const removeLocal = (id: string): void => {
    items.value = items.value.filter((row) => row.id !== id);
    total.value = Math.max(0, total.value - 1);
  };

  const clearFilters = (): void => {
    filterAccountId.value = "";
    filterFrom.value = "";
    filterTo.value = "";
    search.value = "";
    page.value = 1;
  };

  watch([page, ipp, sortBy, filterAccountId, filterFrom, filterTo], () => {
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
    removeLocal,
    clearFilters,
  };
};
