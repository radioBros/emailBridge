import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  createOrganization,
  deleteOrganization,
  inviteMember,
  listOrganizations,
  patchOrganization,
  type OrgCreateInBody,
  type OrgPatchInBody,
  type Organization,
  type OrgQuotaKey,
} from "@/api/organizations";
import { ApiProblemError } from "@/api/client";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { jobProgressText, pollJob } from "@/features/deliveries/helpers/jobPoll";
import type { Job } from "@/api/organizations";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import type { ServerTableSortItem } from "@/components/tables/types";

export interface OrganizationRow extends Record<string, unknown> {
  id: string;
  name: string;
  slug: string;
  status: string;
  memberCount: number;
  accountCount: number;
  createdAt: string;
  version: number;
  raw: Organization;
}

const toRow = (o: Organization): OrganizationRow => ({
  id: o.id,
  name: o.name,
  slug: o.slug,
  status: o.status,
  memberCount: o.memberCount ?? 0,
  accountCount: o.accountCount ?? 0,
  createdAt: o.createdAt,
  version: o.version,
  raw: o,
});

const sortParam = (items: ServerTableSortItem[]): string | undefined => {
  const s = items[0];
  if (!s?.key) return undefined;
  const key = s.key === "createdAt" ? "createdAt" : String(s.key);
  return s.order === "desc" ? `-${key}` : key;
};

const is412 = (err: unknown): boolean =>
  err instanceof ApiProblemError && err.problem.status === 412;

export const useOrganizations = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const session = useSessionStore();
  const notify = useNotifyStore();
  const { messageOf } = useProblem();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const items = ref<OrganizationRow[]>([]);
  const total = ref(0);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.ipp) || 25);
  const search = ref(String(route.query.q ?? ""));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "createdAt", order: "desc" }]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const mutating = ref(false);
  const deleteJob = ref<Job | null>(null);
  const deletePolling = ref(false);
  const createSlugError = ref<string | null>(null);

  const isOwner = computed(() => session.profile?.instanceRole === "owner");
  const roleReady = computed(() => Boolean(session.profile?.instanceRole));

  const tableLoading = computed(() => isRefreshing.value && !isFirstLoad.value);

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
    if (roleReady.value && !isOwner.value) {
      forbidden.value = true;
      items.value = [];
      total.value = 0;
      start();
      finish(false);
      return;
    }

    start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const res = await listOrganizations({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value) ?? "-createdAt",
        q: search.value.trim() || undefined,
      });
      items.value = (res.data ?? []).map(toRow);
      total.value = res.total ?? items.value.length;
      finish(true);
    } catch (e) {
      if (e instanceof ApiProblemError && e.problem.status === 403) {
        forbidden.value = true;
        items.value = [];
        total.value = 0;
        finish(true);
      } else {
        loadError.value = messageOf(e) || t("organizations.load_error");
        finish(false);
      }
    } finally {
      syncQuery();
    }
  };

  watch([page, ipp, sortBy], () => {
    void load();
  });

  const onSearch = (q: string): void => {
    search.value = q;
    page.value = 1;
    void load();
  };

  const setPage = (p: number): void => {
    page.value = p;
  };
  const setIpp = (n: number): void => {
    ipp.value = n;
    page.value = 1;
  };
  const setSortBy = (s: ServerTableSortItem[]): void => {
    sortBy.value = s;
  };

  const handleConflict = async (e: unknown): Promise<void> => {
    notify.push({ message: messageOf(e), color: "warning" });
    await load();
  };

  const clearCreateSlugError = (): void => {
    createSlugError.value = null;
  };

  const create = async (
    body: OrgCreateInBody,
    ownerEmail?: string,
  ): Promise<boolean> => {
    mutating.value = true;
    createSlugError.value = null;
    try {
      const org = await createOrganization(body);
      notify.push({ message: t("organizations.created"), color: "success" });
      if (ownerEmail?.trim()) {
        try {
          await inviteMember(org.id, { email: ownerEmail.trim(), role: "owner" });
          notify.push({ message: t("organizations.invited_owner"), color: "success" });
        } catch (inviteErr) {
          notify.push({
            message: messageOf(inviteErr) || t("organizations.invite_error"),
            color: "warning",
          });
        }
      }
      await load();
      return true;
    } catch (e) {
      if (e instanceof ApiProblemError && e.problem.status === 409) {
        createSlugError.value = t("organizations.slug_taken");
        notify.push({ message: createSlugError.value, color: "error" });
        return false;
      }
      notify.push({ message: messageOf(e) || t("organizations.save_error"), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const patch = async (row: OrganizationRow, body: OrgPatchInBody): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchOrganization(row.id, body, row.version);
      notify.push({ message: t("organizations.updated"), color: "success" });
      await load();
      return true;
    } catch (e) {
      if (is412(e)) {
        await handleConflict(e);
        return false;
      }
      notify.push({ message: messageOf(e) || t("organizations.save_error"), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const updateOrg = async (
    row: OrganizationRow,
    payload: { name: string; status: "active" | "suspended" },
  ): Promise<boolean> =>
    patch(row, { name: payload.name.trim(), status: payload.status });

  const setStatus = async (row: OrganizationRow, status: "active" | "suspended"): Promise<boolean> =>
    patch(row, { status });

  const saveQuotas = async (
    row: OrganizationRow,
    quotas: Partial<Record<OrgQuotaKey, number | null>>,
  ): Promise<boolean> => {
    const body: OrgPatchInBody = { quotas };
    return patch(row, body);
  };

  const remove = async (row: OrganizationRow): Promise<boolean> => {
    if (session.organizationId === row.id) {
      notify.push({ message: t("organizations.delete_active_blocked"), color: "warning" });
      return false;
    }
    mutating.value = true;
    deleteJob.value = null;
    deletePolling.value = false;
    try {
      const out = await deleteOrganization(row.id, row.version);
      const jobId = out.job?.id;
      if (!jobId) {
        notify.push({ message: t("organizations.delete_error"), color: "error" });
        return false;
      }
      deletePolling.value = true;
      deleteJob.value = { ...out.job, state: out.job.state } as Job;
      const final = await pollJob(jobId, (j) => {
        deleteJob.value = j;
      });
      deletePolling.value = false;
      if (final.state === "succeeded") {
        notify.push({ message: t("organizations.deleted"), color: "success" });
        await load();
        return true;
      }
      notify.push({
        message: final.error?.detail || t("organizations.delete_failed"),
        color: "error",
      });
      await load();
      return false;
    } catch (e) {
      deletePolling.value = false;
      if (is412(e)) {
        await handleConflict(e);
        return false;
      }
      notify.push({ message: messageOf(e) || t("organizations.delete_error"), color: "error" });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const deleteProgress = computed(() =>
    deleteJob.value ? jobProgressText(deleteJob.value) : "",
  );

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
    isOwner,
    roleReady,
    tableLoading,
    isFirstLoad,
    isRefreshing,
    deleteJob,
    deletePolling,
    deleteProgress,
    load,
    onSearch,
    setPage,
    setIpp,
    setSortBy,
    createSlugError,
    clearCreateSlugError,
    create,
    updateOrg,
    setStatus,
    saveQuotas,
    remove,
  };
};
