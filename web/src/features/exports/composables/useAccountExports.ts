import { computed, ref } from "vue";
import type { ServerTableSortItem } from "@/components/tables/types";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import {
  cancelExport,
  createExport,
  listExports,
  type ExportCreateInput,
  type ExportView,
} from "@/api/exports";
import { listFeatureSwitches } from "@/api/settings";
import { listMailboxes, type MailboxView } from "@/api/messages";

const SORT_KEYS = new Set(["createdAt", "id"]);

export const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  if (!first?.key || !SORT_KEYS.has(first.key)) return "-createdAt";
  return first.order === "desc" ? `-${first.key}` : first.key;
};

export const useAccountExports = (accountId: () => string) => {
  const { isFirstLoad, isRefreshing, isLoading, start, finish } = useLoadState();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();

  const rows = ref<ExportView[]>([]);
  const total = ref(0);
  const page = ref(1);
  const ipp = ref(25);
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const mutating = ref(false);
  const formError = ref<string | null>(null);
  const enabled = ref(false);
  const featureChecked = ref(false);
  const mailboxes = ref<MailboxView[]>([]);

  const id = computed(() => accountId());

  const refreshFeature = async (): Promise<void> => {
    try {
      const res = await listFeatureSwitches();
      const sw = (res.data ?? []).find((s) => s.name === "export.enabled");
      enabled.value = sw?.effective === true;
    } catch {
      enabled.value = false;
    } finally {
      featureChecked.value = true;
    }
  };

  const loadMailboxes = async (): Promise<void> => {
    if (!id.value) return;
    try {
      const res = await listMailboxes(id.value);
      mailboxes.value = res.data ?? [];
    } catch (err) {
      mailboxes.value = [];
      notify.push({ message: messageOf(err), color: "error" });
    }
  };

  const load = async (): Promise<void> => {
    if (!id.value) return;
    start();
    loadError.value = null;
    try {
      if (!featureChecked.value) await refreshFeature();
      if (!enabled.value) {
        rows.value = [];
        total.value = 0;
        finish(true);
        return;
      }
      const res = await listExports(id.value, {
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
      });
      rows.value = res.items ?? [];
      total.value = res.total ?? rows.value.length;
      finish(true);
    } catch (e) {
      loadError.value = messageOf(e);
      finish(false);
    }
  };

  const setPage = async (v: number): Promise<void> => {
    page.value = v;
    await load();
  };

  const setIpp = async (v: number): Promise<void> => {
    ipp.value = v;
    page.value = 1;
    await load();
  };

  const setSortBy = async (v: ServerTableSortItem[]): Promise<void> => {
    sortBy.value = v;
    page.value = 1;
    await load();
  };

  const create = async (body: ExportCreateInput): Promise<ExportView | null> => {
    mutating.value = true;
    formError.value = null;
    try {
      const accepted = await createExport(id.value, body);
      page.value = 1;
      await load();
      return accepted.export;
    } catch (e) {
      formError.value = messageOf(e);
      return null;
    } finally {
      mutating.value = false;
    }
  };

  const cancel = async (exportId: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await cancelExport(id.value, exportId);
      await load();
      return true;
    } catch (e) {
      loadError.value = messageOf(e);
      return false;
    } finally {
      mutating.value = false;
    }
  };

  return {
    rows,
    total,
    page,
    ipp,
    sortBy,
    loadError,
    mutating,
    formError,
    enabled,
    featureChecked,
    mailboxes,
    isFirstLoad,
    isRefreshing,
    isLoading,
    load,
    refreshFeature,
    loadMailboxes,
    setPage,
    setIpp,
    setSortBy,
    create,
    cancel,
  };
};
