import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  createOauthApp,
  deleteOauthApp,
  listOauthApps,
  patchOauthApp,
  verifyOauthApp,
  type OauthApp,
  type OauthAppCreate,
  type OauthAppPatch,
  type OauthVerifyResult,
} from "@/api/oauth";
import { ApiProblemError } from "@/api/client";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import type { ServerTableSortItem } from "@/components/tables/types";

export interface OauthAppRow extends Record<string, unknown> {
  id: string;
  name: string;
  provider: string;
  clientId: string;
  enabled: boolean;
  version: number;
  createdAt: string;
  raw: OauthApp;
}

const toRow = (a: OauthApp): OauthAppRow => ({
  id: a.id,
  name: a.name,
  provider: a.provider,
  clientId: a.clientId,
  enabled: a.enabled,
  version: a.version,
  createdAt: a.createdAt,
  raw: a,
});

const sortParam = (items: ServerTableSortItem[]): string | undefined => {
  const s = items[0];
  if (!s?.key) return undefined;
  return s.order === "desc" ? `-${s.key}` : String(s.key);
};

export const useOauthApps = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const notify = useNotifyStore();
  const { messageOf } = useProblem();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const items = ref<OauthAppRow[]>([]);
  const total = ref(0);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.ipp) || 25);
  const search = ref(String(route.query.q ?? ""));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const mutating = ref(false);
  const lastVerify = ref<OauthVerifyResult | null>(null);
  const tableLoading = computed(() => isFirstLoad.value || isRefreshing.value);

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

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const res = await listOauthApps({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value) ?? "-createdAt",
        q: search.value || undefined,
      });
      items.value = (res.data ?? []).map(toRow);
      total.value = res.total ?? items.value.length;
      finish(true);
    } catch (e) {
      if (e instanceof ApiProblemError && e.problem.status === 403) {
        forbidden.value = true;
      } else {
        loadError.value = messageOf(e) || t("oauth.load_error");
      }
      items.value = [];
      total.value = 0;
      finish(false);
    }
  };

  watch([page, ipp, sortBy], () => {
    syncQuery();
    void load();
  });

  const onSearch = (q: string): void => {
    search.value = q;
    page.value = 1;
    syncQuery();
    void load();
  };

  const setPage = (p: number): void => {
    page.value = p;
  };
  const setIpp = (n: number): void => {
    ipp.value = n;
    page.value = 1;
  };
  const setSortBy = (s: ServerTableSortItem[]): void => {
    sortBy.value = s;
  };

  const create = async (body: OauthAppCreate): Promise<boolean> => {
    mutating.value = true;
    try {
      await createOauthApp(body);
      notify.push({ message: t("oauth.created"), color: "success" });
      await load();
      return true;
    } catch (e) {
      notify.push({ message: messageOf(e) || t("oauth.save_error"), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const update = async (row: OauthAppRow, body: OauthAppPatch): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchOauthApp(row.id, body, row.version);
      notify.push({ message: t("oauth.updated"), color: "success" });
      await load();
      return true;
    } catch (e) {
      notify.push({ message: messageOf(e) || t("oauth.save_error"), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const remove = async (row: OauthAppRow): Promise<void> => {
    mutating.value = true;
    try {
      await deleteOauthApp(row.id, row.version);
      notify.push({ message: t("oauth.deleted"), color: "success" });
      await load();
    } catch (e) {
      notify.push({ message: messageOf(e) || t("oauth.delete_error"), color: "error" });
    } finally {
      mutating.value = false;
    }
  };

  const verify = async (row: OauthAppRow): Promise<OauthVerifyResult | null> => {
    mutating.value = true;
    try {
      const res = await verifyOauthApp(row.id);
      lastVerify.value = res;
      if (res.ok) notify.push({ message: t("oauth.verify_ok"), color: "success" });
      else notify.push({ message: res.detail || t("oauth.verify_fail"), color: "error" });
      return res;
    } catch (e) {
      notify.push({ message: messageOf(e) || t("oauth.verify_fail"), color: "error" });
      return null;
    } finally {
      mutating.value = false;
    }
  };

  return {
    items,
    total,
    page,
    ipp,
    search,
    sortBy,
    loadError,
    forbidden,
    mutating,
    lastVerify,
    tableLoading,
    isFirstLoad,
    isRefreshing,
    load,
    onSearch,
    setPage,
    setIpp,
    setSortBy,
    create,
    update,
    remove,
    verify,
  };
};
