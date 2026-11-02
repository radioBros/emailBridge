import { ref } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { ApiProblemError } from "@/api/client";
import {
  createWebhookEndpoint,
  deleteWebhookEndpoint,
  listWebhookEndpoints,
  patchWebhookEndpoint,
  rotateWebhookEndpointSecret,
  testWebhookEndpoint,
  type DeliveryAttempt,
  type EndpointCreate,
  type EndpointUpdate,
  type EndpointView,
  type EndpointViewRow,
  type RotateSecretResult,
} from "@/api/webhooks";
import { generateWebhookSecret } from "@/features/webhooks/eventTypes";

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first?.key) return "-createdAt";
  const key = first.key === "url" ? "name" : first.key;
  if (!["name", "createdAt", "id"].includes(key)) return "-createdAt";
  return first.order === "desc" ? `-${key}` : key;
};

export const useWebhooks = () => {
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();
  const { messageOf, fieldErrors } = useProblem();

  const rows = ref<EndpointViewRow[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const search = ref("");
  const sortBy = ref<ServerTableSortItem[]>([{ key: "name", order: "asc" }]);
  const loadError = ref<string | null>(null);
  const mutating = ref(false);
  const formError = ref<string | null>(null);
  const formFieldErrors = ref<Record<string, string>>({});
  const revealedSecret = ref<string | null>(null);
  const testResult = ref<DeliveryAttempt | null>(null);
  const testError = ref<string | null>(null);

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listWebhookEndpoints({
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

  const create = async (body: EndpointCreate): Promise<EndpointView | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    const secret = body.secret?.trim() || generateWebhookSecret();
    try {
      const created = await createWebhookEndpoint({ ...body, secret });
      revealedSecret.value = secret;
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

  const update = async (id: string, body: EndpointUpdate): Promise<EndpointView | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    try {
      const updated = await patchWebhookEndpoint(id, body);
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
      await deleteWebhookEndpoint(id);
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const setEnabled = async (row: EndpointView, enabled: boolean): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchWebhookEndpoint(row.id, { enabled });
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const rotateSecret = async (id: string): Promise<RotateSecretResult | null> => {
    mutating.value = true;
    try {
      const result = await rotateWebhookEndpointSecret(id);
      revealedSecret.value = result.secret;
      await load();
      return result;
    } catch (e) {
      loadError.value = messageOf(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const runTest = async (id: string): Promise<DeliveryAttempt | null> => {
    mutating.value = true;
    testError.value = null;
    testResult.value = null;
    try {
      const result = await testWebhookEndpoint(id);
      testResult.value = result;
      return result;
    } catch (e) {
      testError.value = messageOf(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const clearSecret = (): void => {
    revealedSecret.value = null;
  };

  const clearTest = (): void => {
    testResult.value = null;
    testError.value = null;
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
    revealedSecret,
    testResult,
    testError,
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
    rotateSecret,
    runTest,
    clearSecret,
    clearTest,
  };
};
