import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  exportLogs,
  getLogSettings,
  getLogStats,
  listLogs,
  patchLogSettings,
  searchAccounts,
  type AccountOption,
  type LogLevel,
  type LogRecord,
  type LogSettings,
  type LogStats,
} from "@/api/logs";
import { ApiProblemError } from "@/api/client";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import {
  cursorTotal,
  defaultRange,
} from "@/features/logs/helpers/logFormat";
import {
  ensureAccountLabels,
  rememberAccounts,
} from "@/features/logs/helpers/accountCache";
import { useLogTail } from "@/features/logs/composables/useLogTail";
import { LOG_CATEGORIES } from "@/features/logs/constants";
import type { ServerTableSortItem } from "@/components/tables/types";

export const useLogs = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const session = useSessionStore();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const range = defaultRange();
  const from = ref(String(route.query.from ?? range.from));
  const to = ref(String(route.query.to ?? range.to));
  const categories = ref<string[]>(
    typeof route.query.category === "string"
      ? route.query.category.split(",").filter(Boolean)
      : Array.isArray(route.query.category)
        ? (route.query.category as string[])
        : [],
  );
  const level = ref<LogLevel | "">((route.query.level as LogLevel) || "");
  const accountId = ref(String(route.query.accountId ?? ""));
  const requestId = ref(String(route.query.requestId ?? ""));
  const eventId = ref(String(route.query.eventId ?? ""));
  const deliveryId = ref(String(route.query.deliveryId ?? ""));
  const outboundId = ref(String(route.query.outboundId ?? ""));
  const traceId = ref(String(route.query.traceId ?? ""));
  const search = ref(String(route.query.q ?? ""));

  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 50);
  const sortBy = ref<ServerTableSortItem[]>([{ key: "time", order: "desc" }]);

  const items = ref<LogRecord[]>([]);
  const nextCursor = ref<string | null>(null);
  const cursorStack = ref<(string | undefined)[]>([undefined]);
  const stats = ref<LogStats | null>(null);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const selected = ref<LogRecord | null>(null);
  const drawerOpen = ref(false);
  const settingsOpen = ref(false);
  const logSettings = ref<LogSettings | null>(null);
  const settingsEtag = ref<string | null>(null);
  const settingsSaving = ref(false);
  const exporting = ref(false);
  const accountOptions = ref<AccountOption[]>([]);
  const accountsLoading = ref(false);

  const tail = useLogTail(items);

  const canWriteLogs = computed(() => session.profile?.instanceRole === "owner");
  const canReadLogs = computed(() => {
    const role = session.me?.role;
    return role === "owner" || role === "admin" || role === "operator";
  });

  const total = computed(() =>
    cursorTotal(page.value, ipp.value, items.value.length, Boolean(nextCursor.value)),
  );

  const categoryItems = computed(() =>
    LOG_CATEGORIES.map((c) => ({ title: c, value: c })),
  );

  const levelItems = computed(() =>
    (["trace", "debug", "info", "warn", "error", "fatal"] as LogLevel[]).map((l) => ({
      title: t(`logs.level.${l}`),
      value: l,
    })),
  );

  const filterQuery = computed(() => ({
    from: from.value,
    to: to.value,
    category: categories.value.length ? categories.value : undefined,
    level: level.value || undefined,
    accountId: accountId.value || undefined,
    requestId: requestId.value || undefined,
    eventId: eventId.value || undefined,
    deliveryId: deliveryId.value || undefined,
    outboundId: outboundId.value || undefined,
    traceId: traceId.value || undefined,
    q: search.value || undefined,
  }));

  const syncUrl = (): void => {
    const q: Record<string, string> = {
      from: from.value,
      to: to.value,
      page: String(page.value),
      limit: String(ipp.value),
    };
    if (categories.value.length) q.category = categories.value.join(",");
    if (level.value) q.level = level.value;
    if (accountId.value) q.accountId = accountId.value;
    if (requestId.value) q.requestId = requestId.value;
    if (eventId.value) q.eventId = eventId.value;
    if (deliveryId.value) q.deliveryId = deliveryId.value;
    if (outboundId.value) q.outboundId = outboundId.value;
    if (traceId.value) q.traceId = traceId.value;
    if (search.value) q.q = search.value;
    void router.replace({ query: q });
  };

  const resetCursors = (): void => {
    page.value = 1;
    cursorStack.value = [undefined];
    nextCursor.value = null;
  };

  const loadPage = async (): Promise<void> => {
    if (tail.live.value) return;
    start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const cursor = cursorStack.value[page.value - 1];
      const res = await listLogs({
        ...filterQuery.value,
        limit: ipp.value,
        cursor: cursor || undefined,
      });
      items.value = res.data ?? [];
      nextCursor.value = res.nextCursor ?? null;
      if (nextCursor.value) {
        cursorStack.value[page.value] = nextCursor.value;
      }
      await ensureAccountLabels(
        items.value.map((r) => r.accountId).filter((id): id is string => Boolean(id)),
      );
      syncUrl();
      finish(true);
    } catch (err) {
      if (err instanceof ApiProblemError && err.problem.status === 403) {
        forbidden.value = true;
      } else {
        loadError.value = messageOf(err);
      }
      finish(false);
    }
  };

  const loadStats = async (): Promise<void> => {
    try {
      stats.value = await getLogStats(from.value, to.value);
    } catch {
      stats.value = null;
    }
  };

  const load = async (): Promise<void> => {
    await Promise.all([loadPage(), loadStats()]);
  };

  const onFilterChange = (): void => {
    resetCursors();
    void load();
  };

  const setPage = (value: number): void => {
    page.value = value;
    void loadPage();
  };

  const setIpp = (value: number): void => {
    ipp.value = value;
    resetCursors();
    void loadPage();
  };

  const setSearch = (value: string): void => {
    search.value = value;
    onFilterChange();
  };

  const openDetail = (row: LogRecord): void => {
    selected.value = row;
    drawerOpen.value = true;
  };

  const toggleLive = async (): Promise<void> => {
    if (tail.live.value) {
      tail.stop();
      void loadPage();
      return;
    }
    tail.error.value = null;
    items.value = [];
    await tail.start({
      category: categories.value.length ? categories.value : undefined,
      level: level.value || undefined,
      accountId: accountId.value || undefined,
      q: search.value || undefined,
    });
    if (tail.error.value) {
      notify.push({ message: tail.error.value, color: "error" });
    }
  };

  const doExport = async (): Promise<void> => {
    exporting.value = true;
    try {
      const blob = await exportLogs(filterQuery.value);
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `logs-${from.value.slice(0, 10)}.ndjson`;
      a.click();
      URL.revokeObjectURL(url);
      notify.push({ message: t("logs.export_done"), color: "success" });
    } catch (err) {
      notify.push({ message: messageOf(err), color: "error" });
    } finally {
      exporting.value = false;
    }
  };

  const openSettings = async (): Promise<void> => {
    try {
      const res = await getLogSettings();
      logSettings.value = structuredClone(res.body);
      settingsEtag.value = res.etag;
      settingsOpen.value = true;
    } catch (err) {
      if (err instanceof ApiProblemError && err.problem.status === 403) {
        notify.push({ message: t("logs.settings_forbidden"), color: "warning" });
      } else {
        notify.push({ message: messageOf(err), color: "error" });
      }
    }
  };

  const saveSettings = async (): Promise<void> => {
    if (!logSettings.value) return;
    settingsSaving.value = true;
    try {
      const res = await patchLogSettings(logSettings.value, settingsEtag.value);
      logSettings.value = res.body;
      settingsEtag.value = res.etag;
      settingsOpen.value = false;
      notify.push({ message: t("logs.settings_saved"), color: "success" });
      void loadStats();
    } catch (err) {
      notify.push({ message: messageOf(err), color: "error" });
    } finally {
      settingsSaving.value = false;
    }
  };

  const loadAccounts = async (q: string): Promise<void> => {
    accountsLoading.value = true;
    try {
      accountOptions.value = await searchAccounts(q);
      rememberAccounts(accountOptions.value);
    } catch (err) {
      accountOptions.value = [];
      notify.push({ message: messageOf(err), color: "error" });
    } finally {
      accountsLoading.value = false;
    }
  };

  const idFilterCount = computed(() =>
    [requestId.value, eventId.value, deliveryId.value, outboundId.value, traceId.value].filter(
      Boolean,
    ).length,
  );

  const activeFilterCount = computed(() => {
    let n = idFilterCount.value;
    if (categories.value.length) n += 1;
    if (level.value) n += 1;
    if (accountId.value) n += 1;
    if (from.value || to.value) n += 1;
    return n;
  });

  const clearIdFilters = (): void => {
    requestId.value = "";
    eventId.value = "";
    deliveryId.value = "";
    outboundId.value = "";
    traceId.value = "";
    onFilterChange();
  };

  const clearFilters = (): void => {
    const defaults = defaultRange();
    from.value = defaults.from;
    to.value = defaults.to;
    categories.value = [];
    level.value = "";
    accountId.value = "";
    requestId.value = "";
    eventId.value = "";
    deliveryId.value = "";
    outboundId.value = "";
    traceId.value = "";
    search.value = "";
    onFilterChange();
  };

  const diskCards = computed(() => {
    const disk = stats.value?.disk;
    if (!disk) return [];
    return Object.entries(disk).map(([category, usage]) => ({
      category,
      bytes: usage.bytes,
    }));
  });

  return {
    from,
    to,
    categories,
    level,
    accountId,
    requestId,
    eventId,
    deliveryId,
    outboundId,
    traceId,
    search,
    page,
    ipp,
    sortBy,
    items,
    total,
    nextCursor,
    stats,
    diskCards,
    loadError,
    forbidden,
    selected,
    drawerOpen,
    settingsOpen,
    logSettings,
    settingsSaving,
    exporting,
    accountOptions,
    accountsLoading,
    categoryItems,
    levelItems,
    idFilterCount,
    activeFilterCount,
    isFirstLoad,
    isRefreshing,
    canWriteLogs,
    canReadLogs,
    tail,
    load,
    loadPage,
    onFilterChange,
    setPage,
    setIpp,
    setSearch,
    openDetail,
    toggleLive,
    doExport,
    openSettings,
    saveSettings,
    loadAccounts,
    clearIdFilters,
    clearFilters,
    LOG_CATEGORIES,
  };
};
