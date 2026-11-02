import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  inviteMember,
  listMembers,
  patchMember,
  removeMember,
  type Membership,
} from "@/api/organization";
import { ApiProblemError } from "@/api/client";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useNotifyStore } from "@/stores/notify";
import type { ServerTableSortItem } from "@/components/tables/types";
import { sortParam } from "@/features/organization/helpers/sort";

export interface MemberRow extends Record<string, unknown> {
  id: string;
  name: string;
  email: string;
  role: string;
  lastLoginAt: string | null;
  version: number;
  pending: boolean;
  raw: Membership;
}

const mapMember = (m: Membership): MemberRow => {
  const email = m.email?.trim() || "";
  return {
    id: m.id,
    name: m.name?.trim() || email || m.id,
    email,
    role: m.role,
    lastLoginAt: m.lastLoginAt ?? null,
    version: m.version,
    pending: !m.acceptedAt,
    raw: m,
  };
};

export const useMembers = () => {
  const { t } = useI18n();
  const route = useRoute();
  const router = useRouter();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const items = ref<MemberRow[]>([]);
  const total = ref(0);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.ipp) || 25);
  const search = ref(String(route.query.q ?? ""));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "email", order: "asc" }]);
  const loadError = ref<string | null>(null);
  const forbidden = ref(false);
  const mutating = ref(false);
  const tableLoading = computed(() => isFirstLoad.value || isRefreshing.value);

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
    start();
    loadError.value = null;
    forbidden.value = false;
    try {
      const res = await listMembers({
        page: page.value,
        limit: ipp.value,
        includeTotal: true,
        sort: sortParam(sortBy.value),
        q: search.value.trim() || undefined,
      });
      items.value = (res.data ?? []).map(mapMember);
      total.value = res.total ?? items.value.length;
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
      loadError.value = p?.detail || t("organization.members.load_error");
      finish(false);
    } finally {
      syncQuery();
    }
  };

  const invite = async (email: string, role: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await inviteMember({ email, role });
      notify.push({ message: t("organization.members.invited"), color: "success" });
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("organization.members.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const changeRole = async (row: MemberRow, role: string): Promise<boolean> => {
    mutating.value = true;
    try {
      await patchMember(row.id, { role }, row.version);
      notify.push({ message: t("organization.members.role_saved"), color: "success" });
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("organization.members.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const remove = async (row: MemberRow): Promise<boolean> => {
    mutating.value = true;
    try {
      await removeMember(row.id, row.version);
      notify.push({ message: t("organization.members.removed"), color: "success" });
      await load();
      return true;
    } catch (err) {
      notify.push({
        message: problemOf(err)?.detail || t("organization.members.load_error"),
        color: "error",
      });
      return false;
    } finally {
      mutating.value = false;
    }
  };

  const onSearch = (q: string): void => {
    search.value = q;
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
    sortBy,
    loadError,
    forbidden,
    mutating,
    tableLoading,
    isFirstLoad,
    isRefreshing,
    load,
    invite,
    changeRole,
    remove,
    onSearch,
  };
};
