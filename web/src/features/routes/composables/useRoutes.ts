import { ref } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { ApiProblemError } from "@/api/client";
import {
  createRoute,
  deleteRoute,
  dryRunRoute,
  getJob,
  listRouteVersions,
  listRoutes,
  patchRoute,
  restoreRouteVersion,
  testRoute,
  type Job,
  type Route,
  type RouteCreate,
  type RouteDryRunResult,
  type RouteRow,
  type RouteTestResult,
  type RouteUpdate,
  type RouteVersion,
} from "@/api/routes";
import { listWebhookEndpoints, type EndpointView } from "@/api/webhooks";

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first?.key) return "order";
  const key = first.key === "target" ? "name" : first.key;
  if (!["name", "order", "createdAt", "id"].includes(key)) return "order";
  return first.order === "desc" ? `-${key}` : key;
};

const sleep = (ms: number): Promise<void> =>
  new Promise((resolve) => {
    setTimeout(resolve, ms);
  });

export const useRoutes = () => {
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();
  const { messageOf, fieldErrors } = useProblem();

  const rows = ref<RouteRow[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const search = ref("");
  const sortBy = ref<ServerTableSortItem[]>([{ key: "order", order: "asc" }]);
  const loadError = ref<string | null>(null);
  const mutating = ref(false);
  const formError = ref<string | null>(null);
  const formFieldErrors = ref<Record<string, string>>({});
  const endpoints = ref<EndpointView[]>([]);
  const testResult = ref<RouteTestResult | null>(null);
  const testError = ref<string | null>(null);

  const versions = ref<RouteVersion[]>([]);
  const versionsLoading = ref(false);
  const versionsError = ref<string | null>(null);
  const restoring = ref(false);

  const dryRunJob = ref<Job | null>(null);
  const dryRunResult = ref<RouteDryRunResult | null>(null);
  const dryRunError = ref<string | null>(null);
  const dryRunLoading = ref(false);

  const loadEndpoints = async (): Promise<void> => {
    try {
      const res = await listWebhookEndpoints({ page: 1, limit: 200, includeTotal: false, sort: "name" });
      endpoints.value = res.data ?? [];
    } catch {
      endpoints.value = [];
    }
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listRoutes({
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

  const create = async (body: RouteCreate): Promise<Route | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    try {
      const created = await createRoute(body);
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

  const update = async (id: string, body: RouteUpdate): Promise<Route | null> => {
    mutating.value = true;
    formError.value = null;
    formFieldErrors.value = {};
    try {
      const updated = await patchRoute(id, body);
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
      await deleteRoute(id);
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const setEnabled = async (row: Route, enabled: boolean): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchRoute(row.id, { enabled });
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const runTest = async (
    id: string,
    eventJson: string,
  ): Promise<RouteTestResult | null> => {
    mutating.value = true;
    testError.value = null;
    testResult.value = null;
    try {
      let event: Record<string, unknown> | undefined;
      const trimmed = eventJson.trim();
      if (trimmed) {
        event = JSON.parse(trimmed) as Record<string, unknown>;
      }
      const result = await testRoute(id, { event });
      testResult.value = result;
      return result;
    } catch (e) {
      if (e instanceof SyntaxError) {
        testError.value = "Invalid JSON";
      } else {
        testError.value = messageOf(e);
      }
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const clearTest = (): void => {
    testResult.value = null;
    testError.value = null;
  };

  const loadVersions = async (routeId: string): Promise<void> => {
    versionsLoading.value = true;
    versionsError.value = null;
    try {
      const res = await listRouteVersions(routeId, { page: 1, limit: 50, includeTotal: true });
      versions.value = res.data ?? [];
    } catch (e) {
      versionsError.value = messageOf(e);
      versions.value = [];
    } finally {
      versionsLoading.value = false;
    }
  };

  const restoreVersion = async (routeId: string, version: number): Promise<Route | null> => {
    restoring.value = true;
    versionsError.value = null;
    try {
      const updated = await restoreRouteVersion(routeId, version);
      await load();
      await loadVersions(routeId);
      return updated;
    } catch (e) {
      versionsError.value = messageOf(e);
      return null;
    } finally {
      restoring.value = false;
    }
  };

  const runDryRun = async (
    routeId: string,
    opts: { windowHours: number; limit: number },
  ): Promise<void> => {
    dryRunLoading.value = true;
    dryRunError.value = null;
    dryRunResult.value = null;
    dryRunJob.value = null;
    try {
      const accepted = await dryRunRoute(routeId, {
        windowHours: opts.windowHours,
        limit: opts.limit,
      });
      const job = (accepted as { job?: Job }).job;
      if (!job?.id) {
        dryRunError.value = "No job returned";
        return;
      }
      dryRunJob.value = job;
      for (let i = 0; i < 120; i++) {
        const cur = await getJob(job.id);
        dryRunJob.value = cur;
        if (cur.state === "succeeded") {
          dryRunResult.value = (cur.result as RouteDryRunResult) ?? null;
          break;
        }
        if (cur.state === "failed" || cur.state === "cancelled") {
          dryRunError.value = cur.error?.detail || cur.state;
          break;
        }
        await sleep(500);
      }
    } catch (e) {
      dryRunError.value = messageOf(e);
    } finally {
      dryRunLoading.value = false;
    }
  };

  const clearDryRun = (): void => {
    dryRunJob.value = null;
    dryRunResult.value = null;
    dryRunError.value = null;
  };

  const endpointLabel = (id: string): string => {
    const ep = endpoints.value.find((e) => e.id === id);
    return ep ? `${ep.name} (${ep.kind})` : id;
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
    endpoints,
    testResult,
    testError,
    versions,
    versionsLoading,
    versionsError,
    restoring,
    dryRunJob,
    dryRunResult,
    dryRunError,
    dryRunLoading,
    isFirstLoad,
    isRefreshing,
    isLoading,
    load,
    loadEndpoints,
    setPage,
    setIpp,
    setSortBy,
    setSearch,
    create,
    update,
    remove,
    setEnabled,
    runTest,
    clearTest,
    loadVersions,
    restoreVersion,
    runDryRun,
    clearDryRun,
    endpointLabel,
  };
};
