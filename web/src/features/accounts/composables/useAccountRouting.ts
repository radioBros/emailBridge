import { computed, ref, type Ref } from "vue";
import { listEvents, type EbEvent } from "@/api/events";
import { problemOf } from "@/api/problems";
import {
  listRoutes,
  testRoute,
  type RouteRow,
  type RouteTestResult,
} from "@/api/routes";
import {
  listWebhookEndpoints,
  type EndpointViewRow,
} from "@/api/webhooks";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";

export type RouteTestRow = {
  route: RouteRow;
  endpointName: string;
  result?: RouteTestResult;
  error?: string;
};

/** Org routes + endpoints for an account, with "test against latest event". */
export const useAccountRouting = (accountId: Ref<string>) => {
  const { messageOf } = useProblem();
  const loadState = useLoadState();

  const routes = ref<RouteRow[]>([]);
  const endpoints = ref<EndpointViewRow[]>([]);
  const loadError = ref<string | null>(null);
  const testing = ref(false);
  const testError = ref<string | null>(null);
  const latestEvent = ref<EbEvent | null>(null);
  const testResults = ref<RouteTestRow[]>([]);

  const endpointById = computed(() => {
    const map = new Map<string, EndpointViewRow>();
    for (const e of endpoints.value) map.set(e.id, e);
    return map;
  });

  const endpointLabel = (id: string): string =>
    endpointById.value.get(id)?.name ?? id;

  const rows = computed<RouteTestRow[]>(() => {
    if (testResults.value.length) return testResults.value;
    return routes.value.map((route) => ({
      route,
      endpointName: endpointLabel(route.endpointId),
    }));
  });

  const empty = computed(
    () =>
      !loadState.isLoading.value &&
      !loadError.value &&
      routes.value.length === 0 &&
      endpoints.value.length === 0,
  );

  const load = async (): Promise<void> => {
    if (!accountId.value) return;
    loadState.start();
    loadError.value = null;
    testResults.value = [];
    latestEvent.value = null;
    testError.value = null;
    try {
      const [routeRes, epRes] = await Promise.all([
        listRoutes({ page: 1, limit: 100, includeTotal: true, sort: "order" }),
        listWebhookEndpoints({ page: 1, limit: 100, includeTotal: true, sort: "name" }),
      ]);
      routes.value = routeRes.data ?? [];
      endpoints.value = epRes.data ?? [];
      loadState.finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      loadState.finish(false);
    }
  };

  const testWithLatest = async (): Promise<void> => {
    const id = accountId.value;
    if (!id || !routes.value.length) return;
    testing.value = true;
    testError.value = null;
    testResults.value = [];
    try {
      const evRes = await listEvents({
        accountId: id,
        page: 1,
        limit: 1,
        includeTotal: false,
        sort: "-time",
      });
      const event = (evRes.data ?? [])[0] ?? null;
      latestEvent.value = event;
      if (!event) {
        testError.value = "no_event";
        return;
      }
      const results: RouteTestRow[] = [];
      for (const route of routes.value) {
        const row: RouteTestRow = {
          route,
          endpointName: endpointLabel(route.endpointId),
        };
        try {
          row.result = await testRoute(route.id, { eventId: event.id });
        } catch (err) {
          row.error = messageOf(problemOf(err) ?? err);
        }
        results.push(row);
      }
      testResults.value = results;
    } catch (err) {
      testError.value = messageOf(problemOf(err) ?? err);
    } finally {
      testing.value = false;
    }
  };

  return {
    routes,
    endpoints,
    rows,
    loadError,
    testing,
    testError,
    latestEvent,
    empty,
    isFirstLoad: loadState.isFirstLoad,
    isRefreshing: loadState.isRefreshing,
    endpointLabel,
    load,
    testWithLatest,
  };
};
