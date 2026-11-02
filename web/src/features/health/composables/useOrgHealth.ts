import { ref } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import {
  listAccountHealth,
  type AccountHealthCounts,
  type AccountHealthRow,
  type HealthSeverity,
} from "@/api/health";
import { sortParam } from "@/features/health/helpers/severity";

export const useOrgHealth = () => {
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
  const { messageOf } = useProblem();

  const rows = ref<AccountHealthRow[]>([]);
  const counts = ref<AccountHealthCounts>({
    ok: 0,
    info: 0,
    warning: 0,
    critical: 0,
    total: 0,
  });
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const search = ref("");
  const severity = ref<HealthSeverity | "">("");
  const sortBy = ref<ServerTableSortItem[]>([{ key: "severity", order: "desc" }]);
  const loadError = ref<string | null>(null);

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const first = sortBy.value[0];
      const sort = sortParam(first?.key || "severity", first?.order === "asc" ? "asc" : "desc");
      const res = await listAccountHealth({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort,
        severity: severity.value || undefined,
        q: search.value.trim() || undefined,
      });
      rows.value = res.data ?? [];
      counts.value = res.counts;
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

  const setSearch = async (v: string): Promise<void> => {
    search.value = v;
    page.value = 1;
    await load();
  };

  const setSeverity = async (v: HealthSeverity | ""): Promise<void> => {
    severity.value = v;
    page.value = 1;
    await load();
  };

  return {
    rows,
    counts,
    total,
    page,
    ipp,
    search,
    severity,
    sortBy,
    loadError,
    isFirstLoad,
    isRefreshing,
    load,
    setPage,
    setIpp,
    setSortBy,
    setSearch,
    setSeverity,
  };
};
