import { defineStore } from "pinia";
import { computed, ref } from "vue";
import {
  getNotificationsSummary,
  listNotifications,
  markAllNotificationsRead,
  markNotificationsRead,
  resolveNotification,
  type Notification,
  type NotificationSummary,
} from "@/api/notifications";
import { useLoadState } from "@/composables/useLoadState";
import type { RealtimeMessage } from "@/composables/useRealtime";
import { groupForPanel, highestSeverity, normalizeSummary } from "@/features/notifications/helpers/catalog";

/** Open problems shown in the panel (the centre page has the full list). */
const PANEL_OPEN_LIMIT = 50;
/** Recently resolved problems shown greyed under the open ones. */
const PANEL_RESOLVED_LIMIT = 5;
const CRITICAL_PROBE_LIMIT = 50;

export const NOTIFICATIONS_TOPIC = "notifications";
/** Subscription id of the bell on the shared admin socket (frames echo it back). */
export const BELL_SUBSCRIPTION_ID = "notifications-bell";

const emptySummary = (): NotificationSummary => ({
  unread: 0,
  open: 0,
  bySeverity: { critical: 0, warning: 0, info: 0 },
});

/** The data part of a realtime frame on the notifications topic (a partial summary, no params). */
export interface RealtimeNotification {
  id?: string;
  key?: string;
  severity?: string;
  state?: string;
}

export const realtimeNotificationOf = (msg: RealtimeMessage): RealtimeNotification | null => {
  if (msg.topic !== NOTIFICATIONS_TOPIC && msg.id !== BELL_SUBSCRIPTION_ID) return null;
  if (!msg.type || !msg.type.startsWith("notification.")) return null;
  return msg.data && typeof msg.data === "object" ? (msg.data as RealtimeNotification) : {};
};

/**
 * Shared notification state: the bell badge (summary), the panel rows, and which critical problems
 * the user has already been told about. The bell owns the realtime subscription and feeds it here;
 * the centre page reads `revision` to refetch when something changed.
 */
export const useNotificationFeed = defineStore("notificationFeed", () => {
  const summary = ref<NotificationSummary>(emptySummary());
  const summaryLoaded = ref(false);
  const rows = ref<Notification[]>([]);
  const panelError = ref<unknown>(null);
  const panel = useLoadState();
  /** Bumped on every realtime change; pages watch it to refetch. */
  const revision = ref(0);

  /** Critical keys the user has seen or was toasted about this session (one toast per key). */
  const knownCritical = new Set<string>();
  let primed = false;

  const groups = computed(() => groupForPanel(rows.value));
  const topSeverity = computed(() => highestSeverity(summary.value));

  const refreshSummary = async (): Promise<NotificationSummary> => {
    const next = normalizeSummary(await getNotificationsSummary());
    summary.value = next;
    summaryLoaded.value = true;
    return next;
  };

  const loadPanel = async (): Promise<void> => {
    panel.start();
    panelError.value = null;
    try {
      const [open, resolved] = await Promise.all([
        listNotifications({ state: "open", limit: PANEL_OPEN_LIMIT, sort: "-lastSeen" }),
        listNotifications({ state: "resolved", limit: PANEL_RESOLVED_LIMIT, sort: "-lastSeen" }),
      ]);
      rows.value = [...(open?.data ?? []), ...(resolved?.data ?? [])];
      panel.finish(true);
    } catch (err) {
      panelError.value = err;
      panel.finish(false);
      throw err;
    }
  };

  /**
   * Open critical problems not seen before. The first call only primes the set: signing in to a
   * system that already has critical problems must not fire a burst of toasts.
   */
  const freshCriticals = async (): Promise<Notification[]> => {
    const page = await listNotifications({
      state: "open",
      severity: "critical",
      limit: CRITICAL_PROBE_LIMIT,
      sort: "-lastSeen",
    });
    const fresh: Notification[] = [];
    for (const n of page?.data ?? []) {
      if (!n?.key || knownCritical.has(n.key)) continue;
      knownCritical.add(n.key);
      if (primed) fresh.push(n);
    }
    primed = true;
    return fresh;
  };

  /**
   * Nothing is open at all: any later critical is news, so no probe is needed to prime. (The
   * summary's `bySeverity` counts UNREAD items only, so an open-but-read critical must still be
   * recorded by the probe, or its next watcher bump would toast.)
   */
  const primeIfQuiet = (s: NotificationSummary): void => {
    if (s.open === 0) primed = true;
  };

  const isPrimed = (): boolean => primed;

  const markKnown = (key: string | undefined): void => {
    if (key) knownCritical.add(key);
  };

  const isKnownCritical = (key: string | undefined): boolean => Boolean(key && knownCritical.has(key));

  const setLocalRead = (ids: string[]): void => {
    const set = new Set(ids);
    rows.value = rows.value.map((n) => (set.has(n.id) ? { ...n, unread: false } : n));
  };

  const markRead = async (ids: string[]): Promise<void> => {
    const unread = ids.filter((id) => rows.value.find((n) => n.id === id)?.unread !== false);
    if (!unread.length) return;
    await markNotificationsRead(unread);
    setLocalRead(unread);
    revision.value += 1;
    await refreshSummary();
  };

  const markAllRead = async (): Promise<void> => {
    await markAllNotificationsRead();
    rows.value = rows.value.map((n) => ({ ...n, unread: false }));
    revision.value += 1;
    await refreshSummary();
  };

  const resolve = async (id: string): Promise<Notification> => {
    const out = await resolveNotification(id);
    rows.value = rows.value.map((n) => (n.id === id ? { ...n, ...out } : n));
    revision.value += 1;
    await refreshSummary();
    return out;
  };

  const touch = (): void => {
    revision.value += 1;
  };

  /** Organisation switch / sign-out: nothing of the previous context may linger. */
  const reset = (): void => {
    summary.value = emptySummary();
    summaryLoaded.value = false;
    rows.value = [];
    panelError.value = null;
    panel.reset();
    knownCritical.clear();
    primed = false;
  };

  return {
    summary,
    summaryLoaded,
    rows,
    groups,
    topSeverity,
    panelError,
    panelFirstLoad: panel.isFirstLoad,
    panelRefreshing: panel.isRefreshing,
    panelLoadedOnce: panel.hasLoadedOnce,
    revision,
    refreshSummary,
    loadPanel,
    freshCriticals,
    primeIfQuiet,
    isPrimed,
    markKnown,
    isKnownCritical,
    markRead,
    markAllRead,
    resolve,
    touch,
    reset,
  };
});
