<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiChartTimelineVariant,
  mdiDelete,
  mdiPauseCircle,
  mdiPencil,
  mdiPlayCircle,
  mdiPlus,
} from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import PermissionState from "@/features/organization/components/PermissionState.vue";
import CreateOrganizationDialog from "@/features/organizations/components/CreateOrganizationDialog.vue";
import DeleteOrganizationDialog from "@/features/organizations/components/DeleteOrganizationDialog.vue";
import EditOrganizationDialog from "@/features/organizations/components/EditOrganizationDialog.vue";
import OrgQuotasDialog from "@/features/organizations/components/OrgQuotasDialog.vue";
import {
  useOrganizations,
  type OrganizationRow,
} from "@/features/organizations/composables/useOrganizations";
import { orgStatusColor } from "@/features/organizations/helpers/status";

const { t } = useI18n();
const confirm = useConfirm();
const createAction = actionOf("create");

const {
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
  deleteJob,
  deletePolling,
  deleteProgress,
  createSlugError,
  load,
  onSearch,
  create,
  clearCreateSlugError,
  updateOrg,
  setStatus,
  saveQuotas,
  remove,
} = useOrganizations();

const createOpen = ref(false);
const editOpen = ref(false);
const quotasOpen = ref(false);
const deleteOpen = ref(false);
const active = ref<OrganizationRow | null>(null);

const showForbidden = computed(
  () => forbidden.value || (roleReady.value && !isOwner.value),
);

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("organizations.col_name"), sortable: true, forced: true },
  { key: "slug", title: t("organizations.col_slug"), sortable: true },
  { key: "status", title: t("organizations.col_status"), sortable: false },
  { key: "memberCount", title: t("organizations.col_members"), sortable: false },
  { key: "accountCount", title: t("organizations.col_accounts"), sortable: false },
  { key: "createdAt", title: t("organizations.col_created"), sortable: true },
  { key: "actions", title: t("organizations.col_actions"), sortable: false, align: "end" },
]);

const formatWhen = (iso: string): string => {
  if (!iso) return EMPTY_VALUE;
  return DateUtil.format(iso, "dd/MM/yyyy HH:mm") || EMPTY_VALUE;
};

const statusLabel = (status: string): string => {
  const key = `organizations.status_${status}`;
  return t(key) !== key ? t(key) : status;
};

const openCreate = (): void => {
  createOpen.value = true;
};

const openEdit = (row: OrganizationRow): void => {
  if (row.status === "deleting") return;
  active.value = row;
  editOpen.value = true;
};

const openQuotas = (row: OrganizationRow): void => {
  if (row.status === "deleting") return;
  active.value = row;
  quotasOpen.value = true;
};

const openDelete = (row: OrganizationRow): void => {
  if (row.status === "deleting") return;
  active.value = row;
  deleteOpen.value = true;
};

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  openEdit(row.item as OrganizationRow);
};

const onCreate = async (payload: {
  name: string;
  slug: string;
  ownerEmail: string;
}): Promise<void> => {
  const ok = await create(
    { name: payload.name, slug: payload.slug },
    payload.ownerEmail || undefined,
  );
  if (ok) createOpen.value = false;
};

const onEdit = async (payload: {
  name: string;
  status: "active" | "suspended";
}): Promise<void> => {
  if (!active.value) return;
  if (payload.status === "suspended" && active.value.status !== "suspended") {
    const ok = await confirm({
      title: t("organizations.suspend_title"),
      body: t("organizations.suspend_body", { name: active.value.name }),
      variant: "danger",
      confirmText: t("organizations.suspend"),
    });
    if (!ok) return;
  }
  const saved = await updateOrg(active.value, payload);
  if (saved) {
    editOpen.value = false;
    active.value = null;
  }
};

const onQuotas = async (
  quotas: Parameters<typeof saveQuotas>[1],
): Promise<void> => {
  if (!active.value) return;
  const ok = await saveQuotas(active.value, quotas);
  if (ok) {
    quotasOpen.value = false;
    active.value = null;
  }
};

const onDeleteConfirm = async (): Promise<void> => {
  if (!active.value) return;
  const ok = await remove(active.value);
  if (ok) {
    deleteOpen.value = false;
    active.value = null;
  }
};

const onToggleStatus = async (row: OrganizationRow): Promise<void> => {
  if (row.status === "deleting") return;
  if (row.status === "suspended") {
    await setStatus(row, "active");
    return;
  }
  const ok = await confirm({
    title: t("organizations.suspend_title"),
    body: t("organizations.suspend_body", { name: row.name }),
    variant: "danger",
    confirmText: t("organizations.suspend"),
  });
  if (!ok) return;
  await setStatus(row, "suspended");
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-4 mb-6">
      <div class="min-width-0">
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("organizations.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("organizations.subtitle") }}
        </p>
      </div>
      <v-btn
        v-if="isOwner && !showForbidden"
        :color="createAction.color"
        :prepend-icon="mdiPlus"
        class="flex-shrink-0"
        @click="openCreate"
      >
        {{ t("organizations.create") }}
      </v-btn>
    </div>

    <PermissionState
      v-if="showForbidden"
      :title="t('organizations.no_permission_title')"
      :body="t('organizations.no_permission_body')"
    />

    <v-alert
      v-else-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
      <template #append>
        <v-btn
          :color="actionOf('retry').color"
          @click="load"
        >
          {{ t("organizations.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <TableCard
      v-if="!showForbidden"
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      :headers="headers"
      :items="items"
      :total="total"
      :search="search"
      :loading="isFirstLoad || tableLoading"
      table-name="organizations"
      @update:search="onSearch"
      @click:row="onRowClick"
    >
      <template #no-data>
        <div class="table-fill__empty text-center">
          <div class="text-body-large font-weight-medium mb-1">
            {{ t("organizations.empty") }}
          </div>
          <div class="text-body-medium text-medium-emphasis">
            {{ t("organizations.empty_hint") }}
          </div>
        </div>
      </template>

      <template #[`item.status`]="{ item }">
        <v-chip
          :color="orgStatusColor(String(item.status))"
        >
          {{ statusLabel(String(item.status)) }}
        </v-chip>
      </template>
      <template #[`item.createdAt`]="{ item }">
        {{ formatWhen(String(item.createdAt)) }}
      </template>
      <template #[`item.actions`]="{ item }">
        <div
          class="d-flex justify-end ga-1"
          @click.stop
        >
          <v-btn
            :icon="mdiPencil"
            :color="actionOf('edit').color"
            :aria-label="t('organizations.edit')"
            :disabled="mutating || item.status === 'deleting'"
            @click="openEdit(item as OrganizationRow)"
          />
          <v-btn
            :icon="mdiChartTimelineVariant"
            :color="actionOf('settings').color"
            :aria-label="t('organizations.quotas')"
            :disabled="mutating || item.status === 'deleting'"
            @click="openQuotas(item as OrganizationRow)"
          />
          <v-btn
            v-if="item.status !== 'deleting'"
            :icon="item.status === 'suspended' ? mdiPlayCircle : mdiPauseCircle"
            :color="item.status === 'suspended' ? actionOf('resume').color : actionOf('pause').color"
            :aria-label="
              item.status === 'suspended'
                ? t('organizations.reactivate')
                : t('organizations.suspend')
            "
            :disabled="mutating"
            @click="onToggleStatus(item as OrganizationRow)"
          />
          <v-btn
            :icon="mdiDelete"
            :color="actionOf('delete').color"
            :aria-label="t('organizations.delete')"
            :disabled="mutating || item.status === 'deleting'"
            @click="openDelete(item as OrganizationRow)"
          />
        </div>
      </template>
    </TableCard>

    <CreateOrganizationDialog
      v-model:open="createOpen"
      :loading="mutating"
      :slug-error="createSlugError"
      @submit="onCreate"
      @clear-slug-error="clearCreateSlugError"
    />

    <EditOrganizationDialog
      v-model:open="editOpen"
      :organization="active"
      :loading="mutating"
      @submit="onEdit"
    />

    <OrgQuotasDialog
      v-model:open="quotasOpen"
      :organization="active"
      :loading="mutating"
      @submit="onQuotas"
    />

    <DeleteOrganizationDialog
      v-model:open="deleteOpen"
      :organization="active"
      :loading="mutating"
      :job="deleteJob"
      :polling="deletePolling"
      :progress="deleteProgress"
      @confirm="onDeleteConfirm"
    />
  </div>
</template>
