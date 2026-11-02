<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCancel, mdiDownload } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import DateUtil from "@/helpers/Date";
import { actionOf } from "@/constants/Actions";
import { useNotifyStore } from "@/stores/notify";
import {
  exportDownloadPath,
  type ExportCreateInput,
  type ExportView,
} from "@/api/exports";
import CreateExportDialog from "@/features/exports/components/CreateExportDialog.vue";
import { useAccountExports } from "@/features/exports/composables/useAccountExports";
import {
  canCancelExport,
  canDownloadExport,
  exportStatusOf,
  formatBytes,
  progressPct,
} from "@/features/exports/helpers/states";

const props = defineProps<{
  accountId: string;
}>();

const { t, te } = useI18n();
const confirm = useConfirm();
const notify = useNotifyStore();
const createOpen = ref(false);
const createAction = actionOf("create");
const downloadAction = actionOf("download");
const cancelAction = actionOf("cancel");

const {
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
  load,
  loadMailboxes,
  setPage,
  setIpp,
  setSortBy,
  create,
  cancel,
} = useAccountExports(() => props.accountId);

onMounted(() => {
  void load();
});

watch(
  () => props.accountId,
  () => {
    void load();
  },
);

const headers = computed<ServerTableHeader[]>(() => [
  { key: "createdAt", title: t("exports.col_created"), sortable: true },
  { key: "state", title: t("exports.col_state"), sortable: false },
  { key: "progress", title: t("exports.col_progress"), sortable: false },
  { key: "messageCount", title: t("exports.col_messages"), sortable: false },
  { key: "byteSize", title: t("exports.col_size"), sortable: false },
  { key: "actions", title: t("exports.col_actions"), sortable: false, forced: true, align: "end" },
]);

const stateLabel = (state: string): string => {
  const k = `exports.state_${state}`;
  return te(k) ? t(k) : state;
};

const openCreate = async (): Promise<void> => {
  formError.value = null;
  await loadMailboxes();
  createOpen.value = true;
};

const onCreate = async (body: ExportCreateInput): Promise<void> => {
  const created = await create(body);
  if (created) {
    createOpen.value = false;
    notify.push({ message: t("exports.create_ok"), color: "success" });
  }
};

const onCancel = async (row: ExportView): Promise<void> => {
  const ok = await confirm({
    title: t("exports.cancel"),
    body: t("exports.cancel_confirm"),
    variant: "warning",
    confirmText: t("exports.cancel"),
  });
  if (!ok) return;
  if (await cancel(row.id)) {
    notify.push({ message: t("exports.cancel_ok"), color: "success" });
  }
};

const onDownload = (row: ExportView): void => {
  window.location.assign(exportDownloadPath(props.accountId, row.id));
};

const onPage = (v: number): void => {
  void setPage(v);
};
const onIpp = (v: number): void => {
  void setIpp(v);
};
const onSort = (v: typeof sortBy.value): void => {
  void setSortBy(v);
};
</script>

<template>
  <div>
    <v-alert
      v-if="featureChecked && !enabled"
      type="info"
      class="mb-4"
    >
      {{ t("exports.disabled") }}
    </v-alert>

    <template v-else-if="enabled">
      <div class="d-flex flex-wrap align-center justify-space-between ga-2 mb-4">
        <div>
          <div class="text-title-medium font-weight-bold">
            {{ t("exports.title") }}
          </div>
          <div class="text-body-small text-medium-emphasis">
            {{ t("exports.subtitle") }}
          </div>
        </div>
        <v-btn
          :color="createAction.color"
          :prepend-icon="createAction.icon"
          :loading="mutating"
          @click="openCreate"
        >
          {{ t("exports.create") }}
        </v-btn>
      </div>

      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
      </v-alert>

      <TableCard
        v-model:page="page"
        v-model:ipp="ipp"
        v-model:sort-by="sortBy"
        :headers="headers"
        :items="rows"
        :total="total"
        :loading="isFirstLoad || isRefreshing"
        :searchable="false"
        table-name="account-exports"
        item-value="id"
        @update:page="onPage"
        @update:ipp="onIpp"
        @update:sort-by="onSort"
      >
        <template #[`item.createdAt`]="{ item }">
          {{ DateUtil.formatDateTime(item.createdAt) }}
        </template>
        <template #[`item.state`]="{ item }">
          <v-chip
            :color="exportStatusOf(item.state).color"
            :prepend-icon="exportStatusOf(item.state).icon"
            label
          >
            {{ stateLabel(item.state) }}
          </v-chip>
        </template>
        <template #[`item.progress`]="{ item }">
          <div
            v-if="item.progress"
            class="d-flex flex-column ga-1"
            style="min-width: 120px"
          >
            <span class="text-body-small">
              {{
                t("exports.progress_of", {
                  done: item.progress.done,
                  total: item.progress.total,
                })
              }}
            </span>
            <v-progress-linear
              :model-value="progressPct(item.progress.done, item.progress.total)"
              :color="item.state === 'running' ? 'info' : 'success'"
              height="6"
              rounded
            />
          </div>
          <span
            v-else
            class="text-medium-emphasis"
          >—</span>
        </template>
        <template #[`item.messageCount`]="{ item }">
          {{ item.messageCount ?? "—" }}
        </template>
        <template #[`item.byteSize`]="{ item }">
          {{ formatBytes(item.byteSize) }}
        </template>
        <template #[`item.actions`]="{ item }">
          <div class="d-flex ga-1 justify-end">
            <v-btn
              v-if="canDownloadExport(item.state)"
              :icon="mdiDownload"
              :color="downloadAction.color"
              :aria-label="t('exports.download')"
              @click="onDownload(item)"
            />
            <v-btn
              v-if="canCancelExport(item.state)"
              :icon="mdiCancel"
              :color="cancelAction.color"
              :loading="mutating"
              :aria-label="t('exports.cancel')"
              @click="onCancel(item)"
            />
          </div>
        </template>
        <template #no-data>
          <div class="text-center py-6 text-medium-emphasis">
            <div>{{ t("exports.empty") }}</div>
            <div class="text-body-small">
              {{ t("exports.empty_hint") }}
            </div>
          </div>
        </template>
      </TableCard>

      <CreateExportDialog
        v-model="createOpen"
        :mailboxes="mailboxes"
        :submitting="mutating"
        :error-msg="formError"
        @submit="onCreate"
      />
    </template>
  </div>
</template>
