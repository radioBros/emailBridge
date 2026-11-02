import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import type { ServerTableSortItem } from "@/components/tables/types";
import {
  deleteAccount,
  listAccounts,
  reconnectAccount,
  resumeAccount,
  syncAccount,
  type AccountView,
} from "@/api/accounts";
import { useLoadState } from "@/composables/useLoadState";
import { useRealtime, type RealtimeMessage } from "@/composables/useRealtime";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ACCOUNT_STATE_KEYS } from "@/features/accounts/helpers/accountState";

export type AccountRow = AccountView & Record<string, unknown>;

const SORT_KEYS = new Set(["name", "email", "state", "createdAt", "lastSyncAt"]);

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first || !SORT_KEYS.has(first.key)) return "name";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

/** Accounts table: page/sort/filters mirrored in the URL; live state via `accounts.state`. */
export const useAccountsList = () => {
  const route = useRoute();
  const router = useRouter();
  const { t } = useI18n();
  const notify = useNotifyStore();
  const { messageOf } = useProblem();
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();

  const items = ref<AccountRow[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const search = ref(readQuery(route.query.q));
  const filterState = ref(readQuery(route.query.state));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "name", order: "asc" }]);
  const mutatingId = ref<string | null>(null);

  const empty = computed(() => !isLoading.value && !loadError.value && items.value.length === 0);
  const stateOptions = ACCOUNT_STATE_KEYS;

  const syncUrl = (): void => {
    const query: Record<string, string> = {};
    if (page.value > 1) query.page = String(page.value);
    if (ipp.value !== 25) query.limit = String(ipp.value);
    if (filterState.value) query.state = filterState.value;
    if (search.value) query.q = search.value;
    void router.replace({ query });
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listAccounts({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        state: filterState.value || undefined,
        q: search.value || undefined,
      });
      items.value = (res.data ?? []) as AccountRow[];
      total.value = res.total ?? items.value.length;
      finish(true);
      syncUrl();
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const applyStatePatch = (msg: RealtimeMessage): void => {
    const data = msg.data as { id?: string; state?: string; lastSyncAt?: string } | undefined;
    if (!data?.id || !data.state) return;
    const row = items.value.find((a) => a.id === data.id);
    if (!row) return;
    row.state = data.state;
    if (data.lastSyncAt) row.lastSyncAt = data.lastSyncAt;
  };

  useRealtime({
    subscribe: { id: "accounts-list", topic: "accounts.state" },
    onMessage: (msg) => {
      if (msg.topic === "accounts.state" || msg.type?.startsWith("account.")) {
        applyStatePatch(msg);
      }
    },
    onReconnect: () => {
      void load();
    },
  });

  const runAction = async (
    id: string,
    fn: () => Promise<unknown>,
    okMsg: string,
  ): Promise<boolean> => {
    mutatingId.value = id;
    try {
      await fn();
      notify.push({ message: okMsg, color: "success" });
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: messageOf(problemOf(err) ?? err),
        color: "error",
        code: problemOf(err)?.code,
        traceId: problemOf(err)?.traceId,
      });
      return false;
    } finally {
      mutatingId.value = null;
    }
  };

  const doSync = (id: string, mode: "fast" | "full" = "fast") =>
    runAction(id, () => syncAccount(id, { mode }), t("accounts.toast_sync"));

  const doReconnect = (id: string) =>
    runAction(id, () => reconnectAccount(id), t("accounts.toast_reconnect"));

  const doResume = (id: string) =>
    runAction(id, () => resumeAccount(id), t("accounts.toast_resume"));

  const doDelete = (row: AccountView) =>
    runAction(row.id, () => deleteAccount(row.id, row.version), t("accounts.toast_deleted"));

  watch([page, ipp, sortBy, filterState], () => {
    void load();
  });

  watch(search, () => {
    page.value = 1;
    void load();
  });

  return {
    items,
    total,
    page,
    ipp,
    search,
    sortBy,
    filterState,
    stateOptions,
    loadError,
    empty,
    isFirstLoad,
    isRefreshing,
    isLoading,
    mutatingId,
    load,
    doSync,
    doReconnect,
    doResume,
    doDelete,
  };
};
