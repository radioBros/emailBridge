import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  deleteAccount,
  getAccount,
  listAccountOutbound,
  listProtocolLogs,
  downloadProtocolLogs,
  patchAccount,
  reconnectAccount,
  resumeAccount,
  syncAccount,
  type AccountView,
  type ProtocolLogRecord,
  type SubmitView,
} from "@/api/accounts";
import { useLoadState } from "@/composables/useLoadState";
import { useRealtime } from "@/composables/useRealtime";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import {
  ACCOUNT_SWITCH_KEYS,
  switchOn,
  type AccountSwitchKey,
} from "@/features/accounts/helpers/accountState";
import type { ServerTableSortItem } from "@/components/tables/types";
import type { AccountViewG4 } from "@/features/accounts/helpers/accountForm";
import {
  metadataFieldErrors,
  metadataFromRows,
  metadataMergePatch,
  rowsFromMetadata,
  validateMetadataRows,
  type AccountMetadata,
  type MetadataRow,
} from "@/features/accounts/helpers/accountMetadata";

export type AccountRow = AccountView & Record<string, unknown>;
export type OutboundRow = SubmitView & Record<string, unknown>;

const ACCOUNT_TABS = new Set([
  "overview",
  "oauth",
  "folders",
  "events",
  "outbox",
  "logs",
  "routing",
  "settings",
  "exports",
  "pec",
]);

const readTab = (raw: unknown): string => {
  const v = typeof raw === "string" ? raw : Array.isArray(raw) ? String(raw[0] ?? "") : "";
  return ACCOUNT_TABS.has(v) ? v : "overview";
};

/** Account detail: overview, folders, events, outbox, logs, routing, settings. */
export const useAccountDetail = () => {
  const route = useRoute();
  const router = useRouter();
  const { t } = useI18n();
  const notify = useNotifyStore();
  const { messageOf, fieldErrors } = useProblem();
  const overviewLoad = useLoadState();
  const outboxLoad = useLoadState();
  const logsLoad = useLoadState();

  const accountId = computed(() => String(route.params.id ?? ""));

  const account = ref<AccountView | null>(null);
  const loadError = ref<string | null>(null);
  const mutating = ref(false);
  const tab = ref(readTab(route.query.tab));

  watch(
    () => route.query.tab,
    (v) => {
      const next = readTab(v);
      if (next !== tab.value) tab.value = next;
    },
  );

  watch(tab, (v) => {
    if (readTab(route.query.tab) === v) return;
    const query = { ...route.query } as Record<string, string | string[] | undefined>;
    if (v === "overview") delete query.tab;
    else query.tab = v;
    void router.replace({ query });
  });

  const outboxItems = ref<OutboundRow[]>([]);
  const outboxTotal = ref(0);
  const outboxPage = ref(1);
  const outboxIpp = ref(25);
  const outboxSortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const outboxError = ref<string | null>(null);

  const logItems = ref<ProtocolLogRecord[]>([]);
  const logCursor = ref<string | null>(null);
  const logProto = ref<"" | "imap" | "smtp">("");
  const logFrom = ref("");
  const logTo = ref("");
  const logError = ref<string | null>(null);
  const logDownloading = ref(false);

  const switchesSaving = ref(false);

  /** Settings-tab metadata editor (G4): rows, last server field errors, saving flag. */
  const metadataRows = ref<MetadataRow[]>([]);
  const metadataErrors = ref<Record<string, string>>({});
  const metadataSaving = ref(false);
  const storedMetadata = computed<AccountMetadata>(
    () => (account.value as AccountViewG4 | null)?.metadata ?? {},
  );
  const metadataDirty = computed(
    () => metadataMergePatch(storedMetadata.value, metadataFromRows(metadataRows.value)) !== undefined,
  );
  const metadataValid = computed(() => validateMetadataRows(metadataRows.value).valid);
  const resetMetadata = (): void => {
    metadataRows.value = rowsFromMetadata(storedMetadata.value);
    metadataErrors.value = {};
  };

  const isFirstLoad = overviewLoad.isFirstLoad;
  const isRefreshing = overviewLoad.isRefreshing;

  const recentErrors = computed(() => {
    const a = account.value;
    if (!a?.stateReason) return [];
    return [a.stateReason];
  });

  const load = async (): Promise<void> => {
    const id = accountId.value;
    if (!id) return;
    overviewLoad.start();
    loadError.value = null;
    try {
      const keepEdits = account.value !== null && metadataDirty.value; // a refetch never drops unsaved edits
      account.value = await getAccount(id);
      if (!keepEdits) resetMetadata();
      overviewLoad.finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      overviewLoad.finish(false);
    }
  };

  useRealtime({
    subscribe: () => {
      const id = accountId.value;
      if (!id) return null;
      return { id: `account-${id}`, topic: "accounts.state" };
    },
    onMessage: (msg) => {
      const data = msg.data as { id?: string; state?: string; lastSyncAt?: string } | undefined;
      if (!account.value || data?.id !== account.value.id) return;
      if (data.state) account.value.state = data.state;
      if (data.lastSyncAt) account.value.lastSyncAt = data.lastSyncAt;
    },
    onReconnect: () => {
      void load();
    },
  });

  const loadOutbox = async (): Promise<void> => {
    const id = accountId.value;
    if (!id) return;
    outboxLoad.start();
    outboxError.value = null;
    try {
      const sort = outboxSortBy.value[0];
      const sortStr = sort
        ? sort.order === "desc"
          ? `-${sort.key}`
          : sort.key
        : "-createdAt";
      const res = await listAccountOutbound(id, {
        page: outboxPage.value,
        limit: outboxIpp.value,
        sort: sortStr,
      });
      outboxItems.value = (res.data ?? []) as OutboundRow[];
      outboxTotal.value = res.total ?? outboxItems.value.length;
      outboxLoad.finish(true);
    } catch (err) {
      outboxError.value = messageOf(problemOf(err) ?? err);
      outboxLoad.finish(false);
    }
  };

  const loadLogs = async (append = false): Promise<void> => {
    const id = accountId.value;
    if (!id) return;
    logsLoad.start();
    logError.value = null;
    try {
      const res = await listProtocolLogs(id, {
        limit: 100,
        cursor: append ? (logCursor.value ?? undefined) : undefined,
        proto: logProto.value || undefined,
        from: logFrom.value || undefined,
        to: logTo.value || undefined,
      });
      const rows = res.data ?? [];
      logItems.value = append ? [...logItems.value, ...rows] : rows;
      logCursor.value = res.nextCursor;
      logsLoad.finish(true);
    } catch (err) {
      logError.value = messageOf(problemOf(err) ?? err);
      logsLoad.finish(false);
    }
  };

  const notifyOk = (msg: string): void => {
    notify.push({ message: msg, color: "success" });
  };

  const notifyErr = (err: unknown): void => {
    notify.push({
      message: messageOf(problemOf(err) ?? err),
      color: "error",
      code: problemOf(err)?.code,
      traceId: problemOf(err)?.traceId,
    });
  };

  const withMutate = async (fn: () => Promise<void>, okMsg: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await fn();
      notifyOk(okMsg);
      await load();
      return true;
    } catch (err) {
      notifyErr(err);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const doSync = (mode: "fast" | "full" = "fast") =>
    withMutate(async () => {
      await syncAccount(accountId.value, { mode });
    }, t("accounts.toast_sync"));

  const doReconnect = () =>
    withMutate(async () => {
      await reconnectAccount(accountId.value);
    }, t("accounts.toast_reconnect"));

  const doResume = () =>
    withMutate(async () => {
      await resumeAccount(accountId.value);
    }, t("accounts.toast_resume"));

  const doDelete = () =>
    withMutate(async () => {
      await deleteAccount(accountId.value, account.value?.version);
      await router.push("/accounts");
    }, t("accounts.toast_deleted"));

  const setSwitch = async (key: AccountSwitchKey, value: boolean): Promise<boolean> => {
    if (!account.value) return false;
    switchesSaving.value = true;
    try {
      const next = { ...(account.value.switches ?? {}), [key]: value };
      account.value = await patchAccount(
        accountId.value,
        { switches: next },
        account.value.version,
      );
      notifyOk(t("accounts.toast_saved"));
      return true;
    } catch (err) {
      notifyErr(err);
      return false;
    } finally {
      switchesSaving.value = false;
    }
  };

  /** Saves the metadata rows as a JSON Merge Patch (removed keys → null), guarded by If-Match. */
  const saveMetadata = async (): Promise<boolean> => {
    if (!account.value || !metadataValid.value) return false;
    const patch = metadataMergePatch(storedMetadata.value, metadataFromRows(metadataRows.value));
    if (!patch) return true;
    metadataSaving.value = true;
    metadataErrors.value = {};
    try {
      account.value = await patchAccount(
        accountId.value,
        { metadata: patch } as Parameters<typeof patchAccount>[1],
        account.value.version,
      );
      resetMetadata();
      notifyOk(t("accounts.toast_saved"));
      return true;
    } catch (err) {
      metadataErrors.value = metadataFieldErrors(fieldErrors(problemOf(err)));
      notifyErr(err);
      return false;
    } finally {
      metadataSaving.value = false;
    }
  };

  const isSwitchOn = (key: AccountSwitchKey): boolean =>
    switchOn(account.value?.switches, key);

  const downloadLogs = async (): Promise<void> => {
    logDownloading.value = true;
    try {
      const blob = await downloadProtocolLogs(accountId.value, {
        proto: logProto.value || undefined,
        from: logFrom.value || undefined,
        to: logTo.value || undefined,
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `protocol-logs-${accountId.value}.ndjson`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (err) {
      notifyErr(err);
    } finally {
      logDownloading.value = false;
    }
  };

  return {
    account,
    loadError,
    mutating,
    tab,
    isFirstLoad,
    isRefreshing,
    recentErrors,
    outboxItems,
    outboxTotal,
    outboxPage,
    outboxIpp,
    outboxSortBy,
    outboxError,
    outboxFirstLoad: outboxLoad.isFirstLoad,
    outboxRefreshing: outboxLoad.isRefreshing,
    outboxLoading: outboxLoad.isLoading,
    logItems,
    logCursor,
    logProto,
    logFrom,
    logTo,
    logError,
    logDownloading,
    logsFirstLoad: logsLoad.isFirstLoad,
    logsRefreshing: logsLoad.isRefreshing,
    switchesSaving,
    switchKeys: ACCOUNT_SWITCH_KEYS,
    storedMetadata,
    metadataRows,
    metadataErrors,
    metadataSaving,
    metadataDirty,
    metadataValid,
    resetMetadata,
    saveMetadata,
    load,
    loadOutbox,
    loadLogs,
    doSync,
    doReconnect,
    doResume,
    doDelete,
    setSwitch,
    isSwitchOn,
    downloadLogs,
  };
};
