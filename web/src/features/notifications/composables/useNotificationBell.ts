import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import type { Notification } from "@/api/notifications";
import { useProblem } from "@/composables/useProblem";
import { useRealtime, type RealtimeMessage } from "@/composables/useRealtime";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import { badgeText, routeForLink, severitySpec, titleOf } from "@/features/notifications/helpers/catalog";
import {
  BELL_SUBSCRIPTION_ID,
  NOTIFICATIONS_TOPIC,
  realtimeNotificationOf,
  useNotificationFeed,
} from "@/features/notifications/stores/feed";

/** Summary poll while the socket is down. With the socket up there is no polling at all. */
export const FALLBACK_POLL_MS = 60_000;
/** Realtime bursts (a watcher tick raising several problems) collapse into one refetch. */
const REALTIME_DEBOUNCE_MS = 300;

/**
 * Everything behind the top-bar bell: summary + badge, live updates on the `notifications` topic
 * (polling only while the socket is not open), the panel's data, critical toasts (one per key), and
 * the row actions. Background refreshes stay quiet (the badge keeps its last value); actions the
 * user takes always report failures.
 */
export const useNotificationBell = () => {
  const { t, te } = useI18n();
  const router = useRouter();
  const feed = useNotificationFeed();
  const notify = useNotifyStore();
  const session = useSessionStore();
  const { messageOf } = useProblem();

  const open = ref(false);
  const markingAll = ref(false);

  const reportError = (err: unknown): void => {
    notify.push({ message: messageOf(err), color: "error" });
  };

  const toastCriticals = async (): Promise<void> => {
    try {
      const fresh = await feed.freshCriticals();
      for (const n of fresh) {
        notify.push({
          message: t("notifications.toast_critical", { title: titleOf(n, t, te) }),
          color: "error",
        });
      }
    } catch {
      /* best effort: the badge already shows the problem */
    }
  };

  const refresh = async (opts: { criticals?: boolean } = {}): Promise<void> => {
    try {
      const before = feed.summary.bySeverity.critical;
      const s = await feed.refreshSummary();
      if (!feed.isPrimed()) {
        feed.primeIfQuiet(s);
        if (!feed.isPrimed()) await toastCriticals();
      } else if (opts.criticals || s.bySeverity.critical > before) {
        await toastCriticals();
      }
    } catch {
      /* quiet: background refresh */
    }
    if (open.value) {
      try {
        await feed.loadPanel();
      } catch {
        /* the panel renders its own error state with retry */
      }
    }
  };

  let debounce: ReturnType<typeof setTimeout> | undefined;
  let wantCriticals = false;

  const onRealtime = (msg: RealtimeMessage): void => {
    const data = realtimeNotificationOf(msg);
    if (!data) return;
    feed.touch();
    if (data.severity === "critical" && data.state === "open" && !feed.isKnownCritical(data.key)) {
      wantCriticals = true;
    }
    clearTimeout(debounce);
    debounce = setTimeout(() => {
      const criticals = wantCriticals;
      wantCriticals = false;
      void refresh({ criticals });
    }, REALTIME_DEBOUNCE_MS);
  };

  const { status } = useRealtime({
    subscribe: { id: BELL_SUBSCRIPTION_ID, topic: NOTIFICATIONS_TOPIC },
    onMessage: onRealtime,
    onReconnect: () => {
      void refresh({ criticals: true });
    },
  });

  let poll: ReturnType<typeof setInterval> | undefined;
  const stopPoll = (): void => {
    if (poll) clearInterval(poll);
    poll = undefined;
  };
  const startPoll = (): void => {
    if (poll) return;
    poll = setInterval(() => {
      if (typeof document !== "undefined" && document.hidden) return;
      void refresh();
    }, FALLBACK_POLL_MS);
  };

  watch(
    status,
    (s) => {
      if (s === "open") stopPoll();
      else startPoll();
    },
    { immediate: true },
  );

  watch(
    () => session.organizationId,
    (next, prev) => {
      if (next === prev) return;
      feed.reset();
      void refresh();
    },
  );

  watch(open, (isOpen) => {
    if (!isOpen) return;
    void feed.loadPanel().catch(() => {
      /* inline error state */
    });
  });

  onMounted(() => {
    void refresh();
  });

  onUnmounted(() => {
    stopPoll();
    clearTimeout(debounce);
  });

  const unread = computed(() => feed.summary.unread);
  const badge = computed(() => (unread.value > 0 ? badgeText(unread.value) : ""));
  const badgeColor = computed(() => (feed.topSeverity ? severitySpec(feed.topSeverity).color : "primary"));
  const ariaLabel = computed(() =>
    unread.value > 0
      ? t("notifications.bell_label_unread", { count: unread.value })
      : t("notifications.bell_label"),
  );

  const retryPanel = async (): Promise<void> => {
    try {
      await feed.loadPanel();
    } catch (err) {
      reportError(err);
    }
  };

  /** Row click: go to the problem (when it has a known route) and mark it read. */
  const select = async (n: Notification): Promise<void> => {
    const target = routeForLink(router, n.link);
    if (target) {
      open.value = false;
      void router.push(target);
    }
    if (n.unread === false) return;
    try {
      await feed.markRead([n.id]);
    } catch (err) {
      reportError(err);
    }
  };

  const markAllRead = async (): Promise<void> => {
    if (markingAll.value) return;
    markingAll.value = true;
    try {
      await feed.markAllRead();
    } catch (err) {
      reportError(err);
    } finally {
      markingAll.value = false;
    }
  };

  const openCentre = (): void => {
    open.value = false;
    void router.push("/notifications");
  };

  return {
    feed,
    open,
    status,
    unread,
    badge,
    badgeColor,
    ariaLabel,
    markingAll,
    refresh,
    retryPanel,
    select,
    markAllRead,
    openCentre,
  };
};
