import { computed, ref, type Ref } from "vue";
import { listEvents, type EbEvent } from "@/api/events";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import type { ServerTableSortItem } from "@/components/tables/types";

export type AccountEventRow = EbEvent & Record<string, unknown>;

/** Recent events for one account — server page / sort on time, optional type filter. */
export const useAccountRecentEvents = (accountId: Ref<string>) => {
  const { messageOf } = useProblem();
  const loadState = useLoadState();

  const items = ref<AccountEventRow[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const sortBy = ref<ServerTableSortItem[]>([{ key: "time", order: "desc" }]);
  const filterType = ref("");
  const loadError = ref<string | null>(null);

  const empty = computed(
    () =>
      !loadState.isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      !filterType.value,
  );

  const noResults = computed(
    () =>
      !loadState.isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      Boolean(filterType.value),
  );

  const load = async (): Promise<void> => {
    const id = accountId.value;
    if (!id) return;
    loadState.start();
    loadError.value = null;
    try {
      const sort = sortBy.value[0];
      const sortStr = sort
        ? sort.order === "desc"
          ? `-${sort.key}`
          : sort.key
        : "-time";
      const res = await listEvents({
        accountId: id,
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortStr,
        type: filterType.value || undefined,
      });
      items.value = (res.data ?? []) as AccountEventRow[];
      total.value = res.total ?? items.value.length;
      loadState.finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      loadState.finish(false);
    }
  };

  return {
    items,
    total,
    page,
    ipp,
    sortBy,
    filterType,
    loadError,
    empty,
    noResults,
    isFirstLoad: loadState.isFirstLoad,
    isRefreshing: loadState.isRefreshing,
    isLoading: loadState.isLoading,
    load,
  };
};
