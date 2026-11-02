import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  createApiKey,
  getAccount,
  listApiKeys,
  patchApiKey,
  revokeApiKey,
  type ApiKey,
  type ApiKeyCreateBody,
  type ApiKeyCreated,
  type ApiKeyPatchBody,
} from "@/api/apikeys";
import { ApiProblemError } from "@/api/client";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useNotifyStore } from "@/stores/notify";
import type { ServerTableSortItem } from "@/components/tables/types";
import { sortParam } from "@/features/organization/helpers/sort";
import { maskedKey } from "@/features/organization/helpers/maskedKey";

export interface ApiKeyRow extends Record<string, unknown> {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  accountId: string | null;
  accountName: string | null;
  lastUsedAt: string | null;
  createdAt: string;
  expiresAt: string | null;
  version: number;
  raw: ApiKey;
}

const accountNameCache = new Map<string, string>();

const resolveAccountNames = async (ids: string[]): Promise<Record<string, string>> => {
  const unique = [...new Set(ids.filter(Boolean))];
  const missing = unique.filter((id) => !accountNameCache.has(id));
  await Promise.all(
    missing.map(async (id) => {
      try {
        const acc = await getAccount(id);
        accountNameCache.set(id, acc.name || acc.email || id);
      } catch {
        accountNameCache.set(id, id);
      }
    }),
  );
  const out: Record<string, string> = {};
  for (const id of unique) out[id] = accountNameCache.get(id) ?? id;
  return out;
};

const toRow = (k: ApiKey, names: Record<string, string>): ApiKeyRow => ({
  id: k.id,
  name: k.name,
  prefix: maskedKey(k.prefix, k.last4),
  scopes: k.scopes ?? [],
  accountId: k.accountId ?? null,
  accountName: k.accountId ? (names[k.accountId] ?? null) : null,
  lastUsedAt: k.lastUsedAt ?? null,
  createdAt: k.createdAt,
  expiresAt: k.expiresAt ?? null,
  version: k.version,
  raw: k,
});

export const useApiKeys = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const items = ref<ApiKeyRow[]>([]);
  const total = ref(0);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.ipp) || 25);
  const search = ref(String(route.query.q ?? ""));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const mutating = ref(false);
  const createdToken = ref<ApiKeyCreated | null>(null);
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
      const res = await listApiKeys({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        q: search.value.trim() || undefined,
      });
      const keys = (res.data ?? []).filter((k) => !k.revokedAt);
      const names = await resolveAccountNames(
        keys.map((k) => k.accountId).filter((id): id is string => Boolean(id)),
      );
      items.value = keys.map((k) => toRow(k, names));
      total.value = res.total ?? items.value.length;
      finish(true);
    } catch (err) {
      const p = problemOf(err);
      if (p?.status === 403 || (err instanceof ApiProblemError && err.problem.status === 403)) {
        forbidden.value = true;
        items.value = [];
        total.value = 0;
        finish(true);
        return;
      }
      loadError.value = p?.detail || t("organization.api_keys.load_error");
      finish(false);
    } finally {
      syncQuery();
    }
  };

  const create = async (body: ApiKeyCreateBody): Promise<ApiKeyCreated | null> => {
    mutating.value = true;
    try {
      const created = await createApiKey(body);
      createdToken.value = created;
      notify.push({ message: t("organization.api_keys.created"), color: "success" });
      await load();
      return created;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("organization.api_keys.load_error"),
        color: "error",
      });
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const update = async (row: ApiKeyRow, body: ApiKeyPatchBody): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchApiKey(row.id, body, row.version);
      notify.push({ message: t("organization.api_keys.saved"), color: "success" });
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("organization.api_keys.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const revoke = async (row: ApiKeyRow): Promise<boolean> => {
    mutating.value = true;
    try {
      await revokeApiKey(row.id, row.version);
      notify.push({ message: t("organization.api_keys.revoked"), color: "success" });
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("organization.api_keys.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const clearCreatedToken = (): void => {
    createdToken.value = null;
  };

  const onSearch = (q: string): void => {
    search.value = q;
    page.value = 1;
    void load();
  };

  watch([page, ipp, sortBy], () => {
    void load();
  });

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
    createdToken,
    tableLoading,
    isFirstLoad,
    isRefreshing,
    load,
    create,
    update,
    revoke,
    clearCreatedToken,
    onSearch,
  };
};
