import { ref } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { ApiProblemError } from "@/api/client";
import {
  createSmtpGateway,
  deleteSmtpGateway,
  listSmtpGateways,
  patchSmtpGateway,
  verifySmtpGateway,
  type GatewayCreate,
  type GatewayUpdate,
  type GatewayVerifyResult,
  type GatewayView,
} from "@/api/gateways";

const SORT_KEYS = new Set(["name", "createdAt", "id"]);

export const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first?.key || !SORT_KEYS.has(first.key)) return "-createdAt";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

export const useGateways = () => {
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();
  const { messageOf, fieldErrors } = useProblem();

  const rows = ref<GatewayView[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const search = ref("");
  const sortBy = ref<ServerTableSortItem[]>([{ key: "name", order: "asc" }]);
  const loadError = ref<string | null>(null);
  const mutating = ref(false);
  const formError = ref<string | null>(null);
  const formFieldErrors = ref<Record<string, string>>({});
  const verifyResult = ref<GatewayVerifyResult | null>(null);
  const verifyError = ref<string | null>(null);
  const verifying = ref(false);

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listSmtpGateways({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
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

  const setSearch = async (v: string): Promise<void> => {
    search.value = v;
    page.value = 1;
    await load();
  };

  const create = async (body: GatewayCreate): Promise<GatewayView | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    try {
      const created = await createSmtpGateway(body);
      page.value = 1;
      await load();
      return created;
    } catch (e) {
      formError.value = messageOf(e);
      if (e instanceof ApiProblemError) formFieldErrors.value = fieldErrors(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const update = async (id: string, body: GatewayUpdate): Promise<GatewayView | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    try {
      const updated = await patchSmtpGateway(id, body);
      await load();
      return updated;
    } catch (e) {
      formError.value = messageOf(e);
      if (e instanceof ApiProblemError) formFieldErrors.value = fieldErrors(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const remove = async (id: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await deleteSmtpGateway(id);
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const setEnabled = async (row: GatewayView, enabled: boolean): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchSmtpGateway(row.id, { enabled });
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const runVerify = async (id: string): Promise<GatewayVerifyResult | null> => {
    verifying.value = true;
    verifyError.value = null;
    verifyResult.value = null;
    try {
      const result = await verifySmtpGateway(id);
      verifyResult.value = result;
      return result;
    } catch (e) {
      verifyError.value = messageOf(e);
      return null;
    } finally {
      verifying.value = false;
    }
  };

  const clearVerify = (): void => {
    verifyResult.value = null;
    verifyError.value = null;
  };

  return {
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
    verifyResult,
    verifyError,
    verifying,
    isFirstLoad,
    isRefreshing,
    isLoading,
    load,
    setPage,
    setIpp,
    setSortBy,
    setSearch,
    create,
    update,
    remove,
    setEnabled,
    runVerify,
    clearVerify,
  };
};
