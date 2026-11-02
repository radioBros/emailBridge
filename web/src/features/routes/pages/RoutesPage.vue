<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiRouter } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import type { Route, RouteCreate, RouteUpdate } from "@/api/routes";
import { useRoutes } from "@/features/routes/composables/useRoutes";
import RouteFormDialog from "@/features/routes/components/RouteFormDialog.vue";
import RouteTestPanel from "@/features/routes/components/RouteTestPanel.vue";
import RouteRowActions from "@/features/routes/components/RouteRowActions.vue";
import VersionHistoryDrawer from "@/features/routes/components/VersionHistoryDrawer.vue";
import DryRunPanel from "@/features/routes/components/DryRunPanel.vue";

const { t, locale } = useI18n();
const confirm = useConfirm();
const createAction = actionOf("create");

const {
  rows,
  total,
  page,
  ipp,
  search,
  sortBy,
  loadError,
  mutating,
  formError,
  formFieldErrors,
  endpoints,
  testResult,
  testError,
  versions,
  versionsLoading,
  versionsError,
  restoring,
  dryRunJob,
  dryRunResult,
  dryRunError,
  dryRunLoading,
  isFirstLoad,
  isRefreshing,
  load,
  loadEndpoints,
  setPage,
  setIpp,
  setSortBy,
  setSearch,
  create,
  update,
  remove,
  setEnabled,
  runTest,
  clearTest,
  loadVersions,
  restoreVersion,
  runDryRun,
  clearDryRun,
  endpointLabel,
} = useRoutes();

const formOpen = ref(false);
const editing = ref<Route | null>(null);
const testOpen = ref(false);
const testTarget = ref<Route | null>(null);
const versionsOpen = ref(false);
const versionsTarget = ref<Route | null>(null);

onMounted(async () => {
  await Promise.all([load(), loadEndpoints()]);
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("routes.col_name"), forced: true },
  { key: "order", title: t("routes.col_order") },
  { key: "target", title: t("routes.col_target"), sortable: false },
  { key: "enabled", title: t("routes.col_enabled"), sortable: false },
  { key: "lastError", title: t("routes.col_error"), sortable: false },
  { key: "actions", title: t("routes.col_actions"), sortable: false, align: "end" },
]);

const tableItems = computed(() =>
  rows.value.map((r) => ({
    ...r,
    target: r.endpointId,
  })),
);

const relativeAt = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const errorLabel = (code: string): string => {
  const key = `routes.error_${code}`;
  const label = t(key);
  return label !== key ? label : t("routes.error_generic");
};

const errorTip = (code: string, message: string): string =>
  t("routes.error_tooltip", { code, message });

const openCreate = (): void => {
  editing.value = null;
  formError.value = null;
  formOpen.value = true;
};

const openEdit = (row: Route): void => {
  editing.value = row;
  formError.value = null;
  formOpen.value = true;
};

const onSubmit = async (
  payload: RouteCreate | (RouteUpdate & { id: string }),
): Promise<void> => {
  if ("id" in payload) {
    const { id, ...body } = payload;
    const ok = await update(id, body);
    if (ok) formOpen.value = false;
    return;
  }
  const created = await create(payload);
  if (created) formOpen.value = false;
};

const onDelete = async (row: Route): Promise<void> => {
  const ok = await confirm({
    title: t("routes.delete_title"),
    body: t("routes.delete_body", { name: row.name }),
    variant: "danger",
    confirmText: t("routes.delete"),
  });
  if (!ok) return;
  await remove(row.id);
};

const onToggle = async (row: Route): Promise<void> => {
  await setEnabled(row, !row.enabled);
};

const openTest = (row: Route): void => {
  testTarget.value = row;
  clearTest();
  clearDryRun();
  testOpen.value = true;
};

const openVersions = async (row: Route): Promise<void> => {
  versionsTarget.value = row;
  versionsOpen.value = true;
  await loadVersions(row.id);
};

const onRestore = async (version: number): Promise<void> => {
  if (!versionsTarget.value) return;
  const ok = await confirm({
    title: t("routes.versions_restore_title", { n: version }),
    body: t("routes.versions_restore_body"),
    variant: "warning",
    confirmText: t("routes.versions_restore"),
  });
  if (!ok) return;
  const updated = await restoreVersion(versionsTarget.value.id, version);
  if (updated) versionsTarget.value = updated;
};
</script>

<template>
  <div>
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-6">
      <div class="min-w-0">
        <h1 class="text-h5 font-weight-bold mb-1 d-flex align-center ga-2">
          <v-avatar
            color="accent"
          >
            <v-icon
              :icon="mdiRouter"
            />
          </v-avatar>
          {{ t("routes.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("routes.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        data-testid="routes-create"
        @click="openCreate"
      >
        {{ t("routes.create") }}
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
      :headers="headers"
      :items="tableItems"
      :total="total"
      :page="page"
      :ipp="ipp"
      :sort-by="sortBy"
      :search="search"
      :loading="isFirstLoad || isRefreshing"
      :grid="{ xs: 1, md: 2 }"
      table-name="routes"
      @update:page="setPage"
      @update:ipp="setIpp"
      @update:sort-by="setSortBy"
      @update:search="setSearch"
    >
      <template #toolbar>
        <v-btn
          :color="createAction.color"
          :prepend-icon="createAction.icon"
          class="d-md-none"
          @click="openCreate"
        >
          {{ t("routes.create") }}
        </v-btn>
      </template>

      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("routes.empty") }}
        </div>
      </template>

      <template #card="{ item }">
        <v-card class="d-flex flex-column h-100">
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold text-wrap">
              {{ item.name }}
            </v-card-title>
            <v-card-subtitle class="text-wrap">
              {{ endpointLabel(item.endpointId) }}
            </v-card-subtitle>
          </v-card-item>
          <v-card-text class="flex-grow-1 pt-0">
            <div class="d-flex flex-wrap align-center ga-2 mb-2">
              <v-chip
                :color="item.enabled ? 'success' : 'warning'"
              >
                {{ item.enabled ? t("routes.yes") : t("routes.no") }}
              </v-chip>
              <span class="text-caption text-medium-emphasis">
                {{ t("routes.col_order") }} {{ item.order }}
              </span>
            </div>
            <div
              v-if="item.lastError"
              class="d-flex flex-wrap align-center ga-2"
            >
              <v-chip
                v-tippy="errorTip(item.lastError.code, item.lastError.message)"
                color="error"
              >
                {{ errorLabel(item.lastError.code) }}
              </v-chip>
              <span class="text-caption text-medium-emphasis">
                {{ relativeAt(item.lastError.at) }}
              </span>
            </div>
            <span
              v-else
              class="text-caption text-medium-emphasis"
            >{{ t("routes.last_unknown") }}</span>
          </v-card-text>
          <v-card-actions class="px-4 pb-4 pt-0">
            <RouteRowActions
              :item="item"
              @edit="openEdit(item)"
              @test="openTest(item)"
              @versions="openVersions(item)"
              @toggle="onToggle(item)"
              @delete="onDelete(item)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.target`]="{ item }">
        <span class="text-truncate d-inline-block max-w-100">
          {{ endpointLabel(item.endpointId) }}
        </span>
      </template>

      <template #[`item.enabled`]="{ item }">
        <v-chip
          :color="item.enabled ? 'success' : 'warning'"
        >
          {{ item.enabled ? t("routes.yes") : t("routes.no") }}
        </v-chip>
      </template>

      <template #[`item.lastError`]="{ item }">
        <div
          v-if="item.lastError"
          class="d-flex flex-column ga-1 min-w-0"
        >
          <v-chip
            v-tippy="errorTip(item.lastError.code, item.lastError.message)"
            color="error"
          >
            {{ errorLabel(item.lastError.code) }}
          </v-chip>
          <span class="text-caption text-medium-emphasis">
            {{ relativeAt(item.lastError.at) }}
          </span>
        </div>
        <span
          v-else
          class="text-medium-emphasis"
        >{{ t("routes.last_unknown") }}</span>
      </template>

      <template #[`item.actions`]="{ item }">
        <RouteRowActions
          :item="item"
          @edit="openEdit(item)"
          @test="openTest(item)"
          @versions="openVersions(item)"
          @toggle="onToggle(item)"
          @delete="onDelete(item)"
        />
      </template>
    </TableCard>

    <RouteFormDialog
      v-model="formOpen"
      :route="editing"
      :endpoints="endpoints"
      :submitting="mutating"
      :error-msg="formError"
      :field-errors="formFieldErrors"
      @submit="onSubmit"
    />

    <AppDialog
      v-model="testOpen"
      :max-width="720"
      fullscreen-on-mobile
    >
      <template #title>
        {{ t("routes.tools_title") }}
        <span
          v-if="testTarget"
          class="text-medium-emphasis text-body-2 d-block"
        >
          {{ testTarget.name }}
        </span>
      </template>

      <RouteTestPanel
        v-if="testTarget"
        :result="testResult"
        :error="testError"
        :loading="mutating"
        @run="(json) => runTest(testTarget!.id, json)"
      />
      <v-divider class="my-4" />
      <DryRunPanel
        v-if="testTarget"
        :job="dryRunJob"
        :result="dryRunResult"
        :error="dryRunError"
        :loading="dryRunLoading"
        @run="(opts) => runDryRun(testTarget!.id, opts)"
      />

      <template #footer>
        <v-btn @click="testOpen = false">
          {{ t("routes.cancel") }}
        </v-btn>
      </template>
    </AppDialog>

    <VersionHistoryDrawer
      v-model="versionsOpen"
      :versions="versions"
      :current-version="versionsTarget?.currentVersion ?? 0"
      :loading="versionsLoading"
      :restoring="restoring"
      :error-msg="versionsError"
      @restore="onRestore"
    />
  </div>
</template>
