import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  deleteIntegrationInstance,
  installIntegration,
  listIntegrationInstances,
  listIntegrations,
  patchIntegrationInstance,
  testIntegrationInstance,
  type IntegrationCatalogueItem,
  type IntegrationInstall,
  type IntegrationInstance,
  type IntegrationInstancePatch,
  type IntegrationTestResult,
} from "@/api/integrations";
import { ApiProblemError } from "@/api/client";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import type { ServerTableSortItem } from "@/components/tables/types";
import { groupByCategory } from "@/features/integrations/helpers/schema";

export interface InstanceRow extends Record<string, unknown> {
  id: string;
  name: string;
  type: string;
  enabled: boolean;
  version: number;
  createdAt: string;
  updatedAt: string;
  lastActionAt?: string;
  lastActionOK?: boolean;
  lastActionError?: string;
  gatewayId?: string;
  raw: IntegrationInstance;
}

const toRow = (a: IntegrationInstance): InstanceRow => ({
  id: a.id,
  name: a.name,
  type: a.type,
  enabled: a.enabled,
  version: a.version,
  createdAt: a.createdAt,
  updatedAt: a.updatedAt,
  lastActionAt: a.lastActionAt,
  lastActionOK: a.lastActionOK,
  lastActionError: a.lastActionError,
  gatewayId: a.gatewayId,
  raw: a,
});

const sortParam = (items: ServerTableSortItem[]): string | undefined => {
  const s = items[0];
  if (!s?.key) return undefined;
  return s.order === "desc" ? `-${s.key}` : String(s.key);
};

export const useIntegrations = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const notify = useNotifyStore();
  const { messageOf } = useProblem();
  const catalogueLoad = useLoadState();
  const tableLoad = useLoadState();

  const catalogue = ref<IntegrationCatalogueItem[]>([]);
  const catalogueQ = ref("");
  const catalogueError = ref<string | null>(null);
  const catalogueForbidden = ref(false);

  const rows = ref<InstanceRow[]>([]);
  const total = ref(0);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.ipp) || 25);
  const search = ref(String(route.query.q ?? ""));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const mutating = ref(false);
  const formError = ref<string | null>(null);
  const lastTest = ref<IntegrationTestResult | null>(null);

  const tableLoading = computed(() => tableLoad.isFirstLoad.value || tableLoad.isRefreshing.value);
  const catalogueFirstLoad = computed(() => catalogueLoad.isFirstLoad.value);
  const catalogueRefreshing = computed(() => catalogueLoad.isRefreshing.value);
  const groupedCatalogue = computed(() => groupByCategory(catalogue.value));

  const typeName = (type: string): string => {
    const hit = catalogue.value.find((c) => c.id === type);
    return hit?.name ?? type;
  };

  const syncQuery = (): void => {
    void router.replace({
      query: {
        ...route.query,
        page: String(page.value),
        ipp: String(ipp.value),
        q: search.value || undefined,
      },
    });
  };

  const loadCatalogue = async (): Promise<void> => {
    catalogueLoad.start();
    catalogueError.value = null;
    catalogueForbidden.value = false;
    try {
      const res = await listIntegrations({
        q: catalogueQ.value || undefined,
      });
      catalogue.value = res.data ?? [];
      catalogueLoad.finish(true);
    } catch (e) {
      if (e instanceof ApiProblemError && e.problem.status === 403) {
        catalogueForbidden.value = true;
      } else {
        catalogueError.value = messageOf(e) || t("integrations.load_error");
      }
      catalogue.value = [];
      catalogueLoad.finish(false);
    }
  };

  const loadInstances = async (): Promise<void> => {
    tableLoad.start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const res = await listIntegrationInstances({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value) ?? "-createdAt",
        q: search.value || undefined,
      });
      rows.value = (res.data ?? []).map(toRow);
      total.value = res.total ?? rows.value.length;
      tableLoad.finish(true);
    } catch (e) {
      if (e instanceof ApiProblemError && e.problem.status === 403) {
        forbidden.value = true;
      } else {
        loadError.value = messageOf(e) || t("integrations.load_error");
      }
      rows.value = [];
      total.value = 0;
      tableLoad.finish(false);
    }
  };

  const load = async (): Promise<void> => {
    await Promise.all([loadCatalogue(), loadInstances()]);
  };

  watch([page, ipp, sortBy], () => {
    syncQuery();
    void loadInstances();
  });

  const onSearch = (q: string): void => {
    search.value = q;
    page.value = 1;
    syncQuery();
    void loadInstances();
  };

  const onCatalogueSearch = (q: string): void => {
    catalogueQ.value = q;
    void loadCatalogue();
  };

  const setPage = (p: number): void => {
    page.value = p;
  };
  const setIpp = (n: number): void => {
    ipp.value = n;
  };
  const setSortBy = (s: ServerTableSortItem[]): void => {
    sortBy.value = s;
  };

  const install = async (type: string, body: IntegrationInstall): Promise<boolean> => {
    mutating.value = true;
    formError.value = null;
    try {
      await installIntegration(type, body);
      notify.push({ message: t("integrations.installed"), color: "success" });
      await load();
      return true;
    } catch (e) {
      formError.value = messageOf(e) || t("integrations.install_error");
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const update = async (
    row: InstanceRow,
    body: IntegrationInstancePatch,
  ): Promise<boolean> => {
    mutating.value = true;
    formError.value = null;
    try {
      await patchIntegrationInstance(row.id, body, row.version);
      notify.push({ message: t("integrations.updated"), color: "success" });
      await load();
      return true;
    } catch (e) {
      formError.value = messageOf(e) || t("integrations.update_error");
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const remove = async (row: InstanceRow): Promise<boolean> => {
    mutating.value = true;
    try {
      await deleteIntegrationInstance(row.id, row.version);
      notify.push({ message: t("integrations.uninstalled"), color: "success" });
      await load();
      return true;
    } catch (e) {
      notify.push({ message: messageOf(e) || t("integrations.uninstall_error"), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const setEnabled = async (row: InstanceRow, enabled: boolean): Promise<boolean> =>
    update(row, { enabled });

  const runTest = async (row: InstanceRow): Promise<IntegrationTestResult | null> => {
    mutating.value = true;
    lastTest.value = null;
    try {
      const res = await testIntegrationInstance(row.id);
      lastTest.value = res;
      notify.push({
        message: res.ok ? t("integrations.test_ok") : t("integrations.test_fail"),
        color: res.ok ? "success" : "error",
      });
      return res;
    } catch (e) {
      notify.push({ message: messageOf(e) || t("integrations.test_error"), color: "error" });
      return null;
    } finally {
      mutating.value = false;
    }
  };

  return {
    catalogue,
    groupedCatalogue,
    catalogueQ,
    catalogueError,
    catalogueForbidden,
    catalogueFirstLoad,
    catalogueRefreshing,
    rows,
    total,
    page,
    ipp,
    search,
    sortBy,
    loadError,
    forbidden,
    mutating,
    formError,
    lastTest,
    tableLoading,
    isFirstLoad: computed(() => tableLoad.isFirstLoad.value),
    typeName,
    load,
    loadCatalogue,
    loadInstances,
    onSearch,
    onCatalogueSearch,
    setPage,
    setIpp,
    setSortBy,
    install,
    update,
    remove,
    setEnabled,
    runTest,
  };
};
