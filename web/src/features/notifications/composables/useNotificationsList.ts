import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import type { ServerTableSortItem } from "@/components/tables/types";
import {
  listNotifications,
  NOTIFICATION_CATEGORIES,
  type Notification,
  type NotificationSort,
  type NotificationState,
} from "@/api/notifications";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import { canResolve, isSeverity } from "@/features/notifications/helpers/catalog";
import { useNotificationFeed } from "@/features/notifications/stores/feed";

/** Severity sorts as a plain string on the server (critical < info < warning), so it is not offered. */
export const SORT_KEYS = new Set(["lastSeen", "firstSeen"]);

export const sortParam = (sortBy: ServerTableSortItem[]): NotificationSort => {
  const first = sortBy[0];
  if (!first || !SORT_KEYS.has(first.key)) return "-lastSeen";
  return (first.order === "desc" ? `-${first.key}` : first.key) as NotificationSort;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

const stateOf = (raw: string): NotificationState => (raw === "resolved" ? "resolved" : "open");

/** Realtime bursts collapse into one refetch. */
const REVISION_DEBOUNCE_MS = 400;

/**
 * The notification centre list: server-side page / sort / filters, URL-synced, refetched when the
 * bell's feed reports a change, and the bulk actions (mark read; resolve, info items only).
 */
export const useNotificationsList = () => {
  const route = useRoute();
  const router = useRouter();
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const session = useSessionStore();
  const feed = useNotificationFeed();
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();

  const items = ref<Notification[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const sortBy = ref<ServerTableSortItem[]>([{ key: "lastSeen", order: "desc" }]);
  const filterState = ref<NotificationState>(stateOf(readQuery(route.query.state)));
  const filterCategory = ref<string | null>(
    (NOTIFICATION_CATEGORIES as readonly string[]).includes(readQuery(route.query.category))
      ? readQuery(route.query.category)
      : null,
  );
  const filterSeverity = ref<string | null>(isSeverity(route.query.severity) ? String(route.query.severity) : null);
  const filterUnread = ref(route.query.unread === "1");
  const selected = ref<string[]>([]);
  const bulkBusy = ref<"read" | "resolve" | null>(null);

  const activeFilterCount = computed(
    () =>
      (filterState.value === "resolved" ? 1 : 0) +
      (filterCategory.value ? 1 : 0) +
      (filterSeverity.value ? 1 : 0) +
      (filterUnread.value ? 1 : 0),
  );

  const empty = computed(
    () => !isLoading.value && !loadError.value && items.value.length === 0 && activeFilterCount.value === 0,
  );
  const noResults = computed(
    () => !isLoading.value && !loadError.value && items.value.length === 0 && activeFilterCount.value > 0,
  );

  const selectedItems = computed(() => items.value.filter((n) => selected.value.includes(n.id)));
  const selectedUnread = computed(() => selectedItems.value.filter((n) => n.unread !== false));
  /** Bulk resolve is for info problems only; warning / critical go through the detail drawer. */
  const selectedResolvable = computed(() =>
    selectedItems.value.filter((n) => n.severity === "info" && canResolve(n, session.me)),
  );
  const allOnPageSelected = computed(
    () => items.value.length > 0 && items.value.every((n) => selected.value.includes(n.id)),
  );
  const someOnPageSelected = computed(() => selected.value.length > 0 && !allOnPageSelected.value);

  const syncUrl = (): void => {
    const query: Record<string, string> = {};
    if (page.value > 1) query.page = String(page.value);
    if (ipp.value !== 25) query.limit = String(ipp.value);
    if (filterState.value === "resolved") query.state = "resolved";
    if (filterCategory.value) query.category = filterCategory.value;
    if (filterSeverity.value) query.severity = filterSeverity.value;
    if (filterUnread.value) query.unread = "1";
    void router.replace({ query });
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    try {
      const res = await listNotifications({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        state: filterState.value,
        category: filterCategory.value || undefined,
        severity: filterSeverity.value || undefined,
        unread: filterUnread.value ? true : undefined,
      });
      items.value = res?.data ?? [];
      total.value = typeof res?.total === "number" ? res.total : items.value.length;
      const ids = new Set(items.value.map((n) => n.id));
      selected.value = selected.value.filter((id) => ids.has(id));
      finish(true);
      syncUrl();
    } catch (err) {
      loadError.value = messageOf(err);
      finish(false);
    }
  };

  const onFilterChange = (): void => {
    page.value = 1;
    selected.value = [];
    void load();
  };

  const clearFilters = (): void => {
    filterState.value = "open";
    filterCategory.value = null;
    filterSeverity.value = null;
    filterUnread.value = false;
    onFilterChange();
  };

  const toggle = (id: string, on: boolean): void => {
    const rest = selected.value.filter((x) => x !== id);
    selected.value = on ? [...rest, id] : rest;
  };

  const toggleAllOnPage = (on: boolean): void => {
    selected.value = on ? items.value.map((n) => n.id) : [];
  };

  const report = (err: unknown): void => {
    notify.push({ message: messageOf(err), color: "error" });
  };

  const bulkMarkRead = async (): Promise<void> => {
    const ids = selectedUnread.value.map((n) => n.id);
    if (!ids.length || bulkBusy.value) return;
    bulkBusy.value = "read";
    try {
      await feed.markRead(ids);
      items.value = items.value.map((n) => (ids.includes(n.id) ? { ...n, unread: false } : n));
      selected.value = [];
      notify.push({ message: t("notifications.bulk_read_done", { count: ids.length }), color: "success" });
    } catch (err) {
      report(err);
    } finally {
      bulkBusy.value = null;
    }
  };

  /** No bulk endpoint: one POST per item; partial failures are counted and reported. */
  const bulkResolve = async (): Promise<void> => {
    const ids = selectedResolvable.value.map((n) => n.id);
    if (!ids.length || bulkBusy.value) return;
    bulkBusy.value = "resolve";
    let ok = 0;
    let lastError: unknown = null;
    for (const id of ids) {
      try {
        await feed.resolve(id);
        ok += 1;
      } catch (err) {
        lastError = err;
      }
    }
    bulkBusy.value = null;
    selected.value = [];
    if (ok) notify.push({ message: t("notifications.bulk_resolve_done", { count: ok }), color: "success" });
    if (lastError) {
      notify.push({
        message: t("notifications.bulk_resolve_failed", { count: ids.length - ok, reason: messageOf(lastError) }),
        color: "error",
      });
    }
    await load();
  };

  watch([page, ipp, sortBy], () => {
    void load();
  });

  let debounce: ReturnType<typeof setTimeout> | undefined;
  watch(
    () => feed.revision,
    () => {
      clearTimeout(debounce);
      debounce = setTimeout(() => {
        void load();
      }, REVISION_DEBOUNCE_MS);
    },
  );

  watch(
    () => session.organizationId,
    (next, prev) => {
      if (next !== prev) onFilterChange();
    },
  );

  return {
    items,
    total,
    page,
    ipp,
    sortBy,
    filterState,
    filterCategory,
    filterSeverity,
    filterUnread,
    selected,
    selectedItems,
    selectedUnread,
    selectedResolvable,
    allOnPageSelected,
    someOnPageSelected,
    bulkBusy,
    activeFilterCount,
    empty,
    noResults,
    loadError,
    isFirstLoad,
    isRefreshing,
    load,
    onFilterChange,
    clearFilters,
    toggle,
    toggleAllOnPage,
    bulkMarkRead,
    bulkResolve,
  };
};
