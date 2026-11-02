import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { listAuditLog, type AuditRecord } from "@/api/audit";
import { ApiProblemError } from "@/api/client";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import type { ServerTableSortItem } from "@/components/tables/types";
import { sortParam } from "@/features/organization/helpers/sort";

export interface AuditRow extends Record<string, unknown> {
  id: string;
  at: string;
  actor: string;
  actorId: string;
  action: string;
  target: string;
  ip: string;
  changes: AuditRecord["changes"];
  raw: AuditRecord;
}

const toRow = (r: AuditRecord): AuditRow => ({
  id: r.id,
  at: r.at,
  actor: `${r.actor.type}:${r.actor.id}`,
  actorId: r.actor.id,
  action: r.action,
  target: `${r.target.type}:${r.target.id}`,
  ip: r.actor.ip?.trim() || "—",
  changes: r.changes,
  raw: r,
});

const toIso = (local: string): string | undefined => {
  const raw = local.trim();
  if (!raw) return undefined;
  const d = new Date(raw);
  return Number.isNaN(d.getTime()) ? raw : d.toISOString();
};

export const useAuditLog = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const items = ref<AuditRow[]>([]);
  const total = ref(0);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.ipp) || 25);
  const search = ref(String(route.query.q ?? ""));
  const actorId = ref(String(route.query.actorId ?? ""));
  const action = ref(String(route.query.action ?? ""));
  const targetId = ref(String(route.query.targetId ?? ""));
  const from = ref(String(route.query.from ?? ""));
  const to = ref(String(route.query.to ?? ""));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "at", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const selected = ref<AuditRow | null>(null);
  const tableLoading = computed(() => isFirstLoad.value || isRefreshing.value);

  const syncQuery = (): void => {
    void router.replace({
      query: {
        ...route.query,
        page: String(page.value),
        ipp: String(ipp.value),
        q: search.value || undefined,
        actorId: actorId.value || undefined,
        action: action.value || undefined,
        targetId: targetId.value || undefined,
        from: from.value || undefined,
        to: to.value || undefined,
      },
    });
  };

  const load = async (): Promise<void> => {
    start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const res = await listAuditLog({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value) ?? "-at",
        actorId: actorId.value || undefined,
        action: action.value || undefined,
        targetId: targetId.value || undefined,
        from: toIso(from.value),
        to: toIso(to.value),
      });
      let rows = (res.data ?? []).map(toRow);
      const q = search.value.trim().toLowerCase();
      if (q) {
        rows = rows.filter(
          (r) =>
            r.action.toLowerCase().includes(q) ||
            r.actor.toLowerCase().includes(q) ||
            r.target.toLowerCase().includes(q) ||
            r.ip.toLowerCase().includes(q),
        );
      }
      items.value = rows;
      total.value = res.total ?? rows.length;
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
      loadError.value = p?.detail || t("organization.audit.load_error");
      finish(false);
    } finally {
      syncQuery();
    }
  };

  const applyFilters = (): void => {
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
    actorId,
    action,
    targetId,
    from,
    to,
    sortBy,
    loadError,
    forbidden,
    selected,
    tableLoading,
    isFirstLoad,
    isRefreshing,
    load,
    applyFilters,
  };
};
