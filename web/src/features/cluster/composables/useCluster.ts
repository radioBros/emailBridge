import {
  computed,
  getCurrentInstance,
  onMounted,
  onUnmounted,
  ref,
  watch,
} from "vue";
import { useI18n } from "vue-i18n";
import {
  drainNode,
  getCluster,
  listQueues,
  patchQueue,
  type ClusterNode,
  type ClusterView,
  type QueueView,
} from "@/api/cluster";
import { ApiProblemError } from "@/api/client";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useSessionStore } from "@/stores/session";
import { useNotifyStore } from "@/stores/notify";
import {
  sortAndPage,
  type SortItem,
} from "@/features/cluster/helpers/sortPage";

const REFRESH_MS = 10_000;

const errMessage = (err: unknown): string =>
  problemOf(err)?.detail || problemOf(err)?.title || "error";

/**
 * Cluster + queues for the instance-owner screen.
 * Auto-refreshes every 10 s while the document is visible.
 */
export const useCluster = () => {
  const { t } = useI18n();
  const session = useSessionStore();
  const notify = useNotifyStore();

  const cluster = ref<ClusterView | null>(null);
  const queuesAll = ref<QueueView[]>([]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const nodesLoading = ref(false);
  const queuesLoading = ref(false);
  const mutating = ref(false);

  const page = ref(1);
  const ipp = ref(25);
  const sortBy = ref<SortItem[]>([]);
  const queuePage = ref(1);
  const queueIpp = ref(25);
  const queueSortBy = ref<SortItem[]>([]);

  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const isOwner = computed(
    () => session.profile?.instanceRole === "owner",
  );
  /** Null while /v1/me has not arrived — avoid a false no-permission flash. */
  const roleReady = computed(() => Boolean(session.profile?.instanceRole));

  const nodesAll = computed<ClusterNode[]>(() => cluster.value?.nodes ?? []);
  const leader = computed(() => cluster.value?.leader ?? "");
  const totals = computed(() => cluster.value?.totals ?? { accounts: 0 });
  const nodeCount = computed(
    () => cluster.value?.nodeCount ?? nodesAll.value.length,
  );

  // /v1/cluster and /v1/queues return the whole snapshot (not paginated) — sort+page locally.
  const nodesPaged = computed(() =>
    sortAndPage(
      nodesAll.value as Array<ClusterNode & Record<string, unknown>>,
      sortBy.value,
      page.value,
      ipp.value,
      { dateKeys: ["seenAt", "startedAt"], numberKeys: ["accounts"] },
    ),
  );
  const queuesPaged = computed(() =>
    sortAndPage(
      queuesAll.value as Array<QueueView & Record<string, unknown>>,
      queueSortBy.value,
      queuePage.value,
      queueIpp.value,
      { numberKeys: ["pending", "ackPending", "consumers"] },
    ),
  );

  const nodes = computed(() => nodesPaged.value.rows);
  const queues = computed(() => queuesPaged.value.rows);
  const nodesTotal = computed(() => nodesPaged.value.total);
  const queuesTotal = computed(() => queuesPaged.value.total);

  const tableLoading = computed(
    () => nodesLoading.value && !isFirstLoad.value,
  );
  const queuesTableLoading = computed(
    () => queuesLoading.value && !isFirstLoad.value,
  );

  const load = async (silent = false): Promise<void> => {
    if (roleReady.value && !isOwner.value) {
      forbidden.value = true;
      cluster.value = null;
      queuesAll.value = [];
      if (!silent) {
        start();
        finish(false);
      }
      return;
    }

    if (!silent) {
      start();
      nodesLoading.value = true;
      queuesLoading.value = true;
    }
    loadError.value = null;
    forbidden.value = false;

    try {
      const [c, q] = await Promise.all([getCluster(), listQueues()]);
      cluster.value = c;
      queuesAll.value = q;
      finish(true);
    } catch (err) {
      if (
        problemOf(err)?.status === 403 ||
        (err instanceof ApiProblemError && err.problem.status === 403)
      ) {
        forbidden.value = true;
        cluster.value = null;
        queuesAll.value = [];
        finish(false);
      } else {
        loadError.value = errMessage(err);
        finish(Boolean(cluster.value));
      }
    } finally {
      if (!silent) {
        nodesLoading.value = false;
        queuesLoading.value = false;
      }
    }
  };

  const drain = async (node: ClusterNode): Promise<boolean> => {
    mutating.value = true;
    try {
      await drainNode(node.id);
      notify.push({
        message: t("cluster.drain_done", { id: node.id }),
        color: "success",
      });
      await load(true);
      return true;
    } catch (err) {
      notify.push({ message: errMessage(err), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const setPaused = async (queue: QueueView, paused: boolean): Promise<boolean> => {
    mutating.value = true;
    try {
      const updated = await patchQueue(queue.name, { paused });
      queuesAll.value = queuesAll.value.map((q) =>
        q.name === updated.name ? { ...q, ...updated } : q,
      );
      notify.push({
        message: paused
          ? t("cluster.queue_paused", { name: queue.name })
          : t("cluster.queue_resumed", { name: queue.name }),
        color: "success",
      });
      return true;
    } catch (err) {
      notify.push({ message: errMessage(err), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const setKeep = async (queue: QueueView, keep: string): Promise<boolean> => {
    mutating.value = true;
    try {
      const updated = await patchQueue(queue.name, { keep });
      queuesAll.value = queuesAll.value.map((q) =>
        q.name === updated.name ? { ...q, ...updated } : q,
      );
      notify.push({
        message: t("cluster.queue_keep_saved", { name: queue.name }),
        color: "success",
      });
      return true;
    } catch (err) {
      notify.push({ message: errMessage(err), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  let timer: ReturnType<typeof setInterval> | null = null;

  const stopTimer = (): void => {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  };

  const startTimer = (): void => {
    stopTimer();
    timer = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      if (forbidden.value || mutating.value) return;
      void load(true);
    }, REFRESH_MS);
  };

  const onVisibility = (): void => {
    if (document.visibilityState === "visible" && isOwner.value && !forbidden.value) {
      void load(true);
      startTimer();
    } else {
      stopTimer();
    }
  };

  watch(sortBy, () => {
    page.value = 1;
  });
  watch(queueSortBy, () => {
    queuePage.value = 1;
  });

  if (getCurrentInstance()) {
    onMounted(() => {
      void load();
      startTimer();
      document.addEventListener("visibilitychange", onVisibility);
    });

    onUnmounted(() => {
      stopTimer();
      document.removeEventListener("visibilitychange", onVisibility);
    });

    watch([isOwner, roleReady], ([ok, ready]) => {
      if (!ready) return;
      if (ok) {
        void load();
        startTimer();
      } else {
        forbidden.value = true;
        stopTimer();
      }
    });
  }

  return {
    cluster,
    nodes,
    queues,
    nodesTotal,
    queuesTotal,
    leader,
    totals,
    nodeCount,
    loadError,
    forbidden,
    isOwner,
    isFirstLoad,
    isRefreshing,
    tableLoading,
    queuesTableLoading,
    mutating,
    page,
    ipp,
    sortBy,
    queuePage,
    queueIpp,
    queueSortBy,
    load,
    drain,
    setPaused,
    setKeep,
  };
};
