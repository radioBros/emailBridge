import { computed, ref, watch } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { listAccountPecReceipts, type Receipt } from "@/api/pec";

const SORT_KEYS = new Set(["at", "type"]);

export const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first?.key || !SORT_KEYS.has(first.key)) return "-at";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

export const usePecReceipts = (accountId: () => string) => {
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
  const { messageOf } = useProblem();

  const rows = ref<Receipt[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const sortBy = ref<ServerTableSortItem[]>([{ key: "at", order: "desc" }]);
  const typeFilter = ref<string | null>(null);
  const search = ref("");
  const loadError = ref<string | null>(null);

  const id = computed(() => accountId());

  const load = async (): Promise<void> => {
    if (!id.value) return;
    start();
    loadError.value = null;
    try {
      const res = await listAccountPecReceipts(id.value, {
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        type: typeFilter.value || undefined,
        q: search.value.trim() || undefined,
      });
      rows.value = res.data ?? [];
      total.value = res.total ?? rows.value.length;
      finish(true);
    } catch (e) {
      loadError.value = messageOf(e);
      finish(false);
    }
  };

  const setPage = async (v: number): Promise<void> => {
    page.value = v;
    await load();
  };

  const setIpp = async (v: number): Promise<void> => {
    ipp.value = v;
    page.value = 1;
    await load();
  };

  const setSortBy = async (v: ServerTableSortItem[]): Promise<void> => {
    sortBy.value = v;
    page.value = 1;
    await load();
  };

  const setTypeFilter = async (v: string | null): Promise<void> => {
    typeFilter.value = v;
    page.value = 1;
    await load();
  };

  const setSearch = async (v: string): Promise<void> => {
    search.value = v;
    page.value = 1;
    await load();
  };

  watch(
    () => id.value,
    () => {
      page.value = 1;
      void load();
    },
  );

  return {
    rows,
    total,
    page,
    ipp,
    sortBy,
    typeFilter,
    search,
    loadError,
    isFirstLoad,
    isRefreshing,
    load,
    setPage,
    setIpp,
    setSortBy,
    setTypeFilter,
    setSearch,
  };
};
