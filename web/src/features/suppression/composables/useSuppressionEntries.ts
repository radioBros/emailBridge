import { ref } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { ApiProblemError } from "@/api/client";
import {
  createSuppressionEntry,
  deleteSuppressionEntry,
  exportSuppressionEntries,
  getSuppressionList,
  importSuppressionEntries,
  listSuppressionEntries,
  type SuppressionEntryCreate,
  type SuppressionEntryView,
  type SuppressionImportResult,
  type SuppressionListView,
} from "@/api/suppression";

export const useSuppressionEntries = () => {
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();
  const { messageOf, fieldErrors } = useProblem();

  const list = ref<SuppressionListView | null>(null);
  const rows = ref<SuppressionEntryView[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const search = ref("");
  const sortBy = ref<ServerTableSortItem[]>([{ key: "at", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const mutating = ref(false);
  const formError = ref<string | null>(null);
  const formFieldErrors = ref<Record<string, string>>({});
  const importResult = ref<SuppressionImportResult | null>(null);

  const loadList = async (listId: string): Promise<boolean> => {
    try {
      list.value = await getSuppressionList(listId);
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    }
  };

  const load = async (listId: string): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listSuppressionEntries(listId, {
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
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

  const setPage = async (listId: string, v: number): Promise<void> => {
    page.value = v;
    await load(listId);
  };

  const setIpp = async (listId: string, v: number): Promise<void> => {
    ipp.value = v;
    page.value = 1;
    await load(listId);
  };

  const setSearch = async (listId: string, v: string): Promise<void> => {
    search.value = v;
    page.value = 1;
    await load(listId);
  };

  const add = async (
    listId: string,
    body: SuppressionEntryCreate,
  ): Promise<SuppressionEntryView | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    try {
      const created = await createSuppressionEntry(listId, body);
      page.value = 1;
      await load(listId);
      return created;
    } catch (e) {
      formError.value = messageOf(e);
      if (e instanceof ApiProblemError) formFieldErrors.value = fieldErrors(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const remove = async (listId: string, entryId: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await deleteSuppressionEntry(listId, entryId);
      await load(listId);
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const importCsv = async (
    listId: string,
    csv: string,
  ): Promise<SuppressionImportResult | null> => {
    mutating.value = true;
    formError.value = null;
    importResult.value = null;
    try {
      const res = await importSuppressionEntries(listId, csv);
      importResult.value = res;
      page.value = 1;
      await load(listId);
      return res;
    } catch (e) {
      formError.value = messageOf(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const exportCsv = async (listId: string): Promise<string | null> => {
    mutating.value = true;
    try {
      return await exportSuppressionEntries(listId);
    } catch (e) {
      loadError.value = messageOf(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  return {
    list,
    rows,
    total,
    page,
    ipp,
    search,
    sortBy,
    loadError,
    mutating,
    formError,
    formFieldErrors,
    importResult,
    isFirstLoad,
    isRefreshing,
    isLoading,
    loadList,
    load,
    setPage,
    setIpp,
    setSearch,
    add,
    remove,
    importCsv,
    exportCsv,
  };
};
