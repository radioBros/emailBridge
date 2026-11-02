<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiCheckCircle,
  mdiFlaskOutline,
  mdiMagnify,
  mdiPower,
  mdiTrashCan,
} from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { useConfirm } from "@/composables/useConfirm";
import { useNotifyStore } from "@/stores/notify";
import { actionOf } from "@/constants/Actions";
import { EMPTY_VALUE } from "@/helpers/Format";
import DateUtil from "@/helpers/Date";
import type { IntegrationCatalogueItem } from "@/api/integrations";
import {
  useIntegrations,
  type InstanceRow,
} from "@/features/integrations/composables/useIntegrations";
import InstallDialog from "@/features/integrations/components/InstallDialog.vue";

const { t, te } = useI18n();
const confirm = useConfirm();
const deleteAction = actionOf("delete");
const createAction = actionOf("create");

const {
  groupedCatalogue,
  catalogueQ,
  catalogueError,
  catalogueForbidden,
  catalogueFirstLoad,
  catalogueRefreshing,
  catalogue,
  rows,
  total,
  page,
  ipp,
  search,
  sortBy,
  loadError,
  forbidden,
  mutating,
  formError,
  tableLoading,
  typeName,
  load,
  onSearch,
  onCatalogueSearch,
  setPage,
  setIpp,
  setSortBy,
  install,
  remove,
  setEnabled,
  runTest,
} = useIntegrations();

const installOpen = ref(false);
const installTarget = ref<IntegrationCatalogueItem | null>(null);

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("integrations.col_name"), forced: true },
  { key: "type", title: t("integrations.col_type") },
  { key: "enabled", title: t("integrations.col_enabled"), sortable: false },
  { key: "lastAction", title: t("integrations.col_last_action"), sortable: false },
  { key: "updatedAt", title: t("integrations.col_updated") },
  // Default sort key: a header of its own so the phone sort select shows a label, not "createdAt".
  { key: "createdAt", title: t("integrations.col_created"), sortable: true },
  { key: "actions", title: t("integrations.col_actions"), sortable: false, align: "end" },
]);


const categoryLabel = (c: string): string => {
  const key = `integrations.cat_${c}`;
  return te(key) ? t(key) : c;
};

/** Prefer localised catalogue copy; fall back to the plain API description. */
const connectorDescription = (item: IntegrationCatalogueItem): string => {
  const key = `integrations.desc_${item.id}`;
  return te(key) ? t(key) : (item.description ?? "");
};

const openInstall = (item: IntegrationCatalogueItem): void => {
  installTarget.value = item;
  formError.value = null;
  installOpen.value = true;
};

const onInstall = async (payload: {
  name?: string;
  config: Record<string, unknown>;
  secrets: Record<string, string>;
  runTest: boolean;
}): Promise<void> => {
  if (!installTarget.value) return;
  const ok = await install(installTarget.value.id, {
    name: payload.name,
    config: payload.config,
    secrets: Object.keys(payload.secrets).length ? payload.secrets : undefined,
    runTest: payload.runTest,
  });
  if (ok) {
    installOpen.value = false;
    installTarget.value = null;
  }
};

const onUninstall = async (row: InstanceRow): Promise<void> => {
  const ok = await confirm({
    title: t("integrations.uninstall_title"),
    body: t("integrations.uninstall_body", { name: row.name }),
    variant: "danger",
    confirmText: t("integrations.uninstall"),
  });
  if (!ok) return;
  await remove(row);
};

const notify = useNotifyStore();

// The composable writes failures to `formError`, which only the (closed) install dialog shows —
// surface a failed toggle as a notification instead of a silent no-op.
const onToggle = async (row: InstanceRow): Promise<void> => {
  await setEnabled(row, !row.enabled);
  if (formError.value) {
    notify.push({ message: formError.value, color: "error" });
    formError.value = null;
  }
};

const formatWhen = (iso: string): string => {
  if (!iso) return EMPTY_VALUE;
  return DateUtil.format(iso, "dd/MM/yyyy HH:mm") || EMPTY_VALUE;
};

const lastActionLabel = (row: InstanceRow): string => {
  if (!row.lastActionAt) return EMPTY_VALUE;
  const when = formatWhen(row.lastActionAt);
  if (row.lastActionOK === true) return t("integrations.last_action_ok", { when });
  if (row.lastActionOK === false) {
    return t("integrations.last_action_err", {
      when,
      err: row.lastActionError || t("integrations.last_action_failed"),
    });
  }
  return when;
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-4 mb-6">
      <div class="min-width-0">
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("integrations.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("integrations.subtitle") }}
        </p>
      </div>
    </div>

    <v-alert
      v-if="catalogueForbidden || forbidden"
      type="warning"
      class="mb-4"
    >
      {{ t("integrations.forbidden") }}
    </v-alert>
    <v-alert
      v-else-if="catalogueError || loadError"
      type="error"
      class="mb-4"
    >
      {{ catalogueError || loadError }}
    </v-alert>

    <section class="mb-8">
      <div class="d-flex flex-wrap align-center justify-space-between ga-3 mb-4">
        <h2 class="text-h6 font-weight-bold mb-0">
          {{ t("integrations.catalogue") }}
        </h2>
        <v-text-field
          :model-value="catalogueQ"
          :prepend-inner-icon="mdiMagnify"
          :placeholder="t('integrations.search_catalogue')"
          class="no-validation catalogue-search"
          style="max-width: 280px"
          hide-details
          clearable
          no-validation
          @update:model-value="onCatalogueSearch(String($event ?? ''))"
        />
      </div>

      <LoadingSkeleton
        v-if="catalogueFirstLoad"
        :rows="3"
        shape="card"
      />
      <div
        v-else
        class="position-relative"
      >
        <AppSectionLoader :status="catalogueRefreshing" />
        <div
          v-if="!catalogue.length"
          class="text-medium-emphasis py-6"
        >
          {{ t("integrations.catalogue_empty") }}
        </div>
        <div
          v-for="group in groupedCatalogue"
          :key="group.category"
          class="mb-6"
        >
          <div class="text-subtitle-2 text-medium-emphasis mb-3 text-uppercase">
            {{ categoryLabel(group.category) }}
          </div>
          <v-row>
            <v-col
              v-for="item in group.items"
              :key="item.id"
              cols="12"
              sm="6"
              md="4"
            >
              <AppCard class="h-100">
                <div class="d-flex align-start ga-3 mb-3">
                  <v-avatar
                    color="primary"
                    rounded="lg"
                  >
                    <span class="text-caption font-weight-bold">
                      {{ item.name.slice(0, 2).toUpperCase() }}
                    </span>
                  </v-avatar>
                  <div class="min-width-0 flex-grow-1">
                    <div class="text-subtitle-1 font-weight-bold">
                      {{ item.name }}
                    </div>
                    <div class="text-caption text-medium-emphasis">
                      v{{ item.version }}
                      <span v-if="item.installedCount">
                        · {{ t("integrations.installed_count", { n: item.installedCount }) }}
                      </span>
                    </div>
                  </div>
                </div>
                <p class="text-body-2 text-medium-emphasis mb-2">
                  {{ connectorDescription(item) }}
                </p>
                <p
                  v-if="item.runtimeHint"
                  class="text-caption text-medium-emphasis mb-4"
                >
                  {{ item.runtimeHint }}
                </p>
                <v-btn
                  :color="createAction.color"
                  block
                  @click="openInstall(item)"
                >
                  {{ t("integrations.install") }}
                </v-btn>
              </AppCard>
            </v-col>
          </v-row>
        </div>
      </div>
    </section>

    <section>
      <h2 class="text-h6 font-weight-bold mb-4">
        {{ t("integrations.instances") }}
      </h2>
      <TableCard
        :headers="headers"
        :items="rows"
        :total="total"
        :page="page"
        :ipp="ipp"
        :sort-by="sortBy"
        :search="search"
        :loading="tableLoading"
        table-name="integrations-instances"
        @update:page="setPage"
        @update:ipp="setIpp"
        @update:sort-by="setSortBy"
        @update:search="onSearch"
      >
        <template #[`item.type`]="{ item }">
          {{ typeName(String(item.type)) }}
        </template>
        <template #[`item.enabled`]="{ item }">
          <v-chip
            :color="item.enabled ? 'success' : 'default'"
          >
            {{ item.enabled ? t("integrations.enabled") : t("integrations.disabled") }}
          </v-chip>
        </template>
        <template #[`item.lastAction`]="{ item }">
          <span
            :class="item.lastActionOK === false ? 'text-error' : undefined"
            class="text-body-2"
          >
            {{ lastActionLabel(item as InstanceRow) }}
          </span>
          <div
            v-if="item.gatewayId"
            class="text-caption text-medium-emphasis"
          >
            {{ t("integrations.gateway_link", { id: item.gatewayId }) }}
          </div>
        </template>
        <template #[`item.updatedAt`]="{ item }">
          {{ formatWhen(String(item.updatedAt)) }}
        </template>
        <template #[`item.createdAt`]="{ item }">
          {{ formatWhen(String(item.createdAt)) }}
        </template>
        <template #[`item.actions`]="{ item }">
          <div class="d-flex justify-end ga-1">
            <v-btn
              :icon="mdiFlaskOutline"
              :aria-label="t('integrations.test')"
              :disabled="mutating"
              :color="'info'"
              @click="runTest(item as InstanceRow)"
            />
            <v-btn
              :icon="item.enabled ? mdiPower : mdiCheckCircle"
              :aria-label="item.enabled ? t('integrations.disable') : t('integrations.enable')"
              :disabled="mutating"
              :color="item.enabled ? 'warning' : 'success'"
              @click="onToggle(item as InstanceRow)"
            />
            <v-btn
              :icon="mdiTrashCan"
              :aria-label="t('integrations.uninstall')"
              :disabled="mutating"
              :color="deleteAction.color"
              @click="onUninstall(item as InstanceRow)"
            />
          </div>
        </template>
      </TableCard>
    </section>

    <InstallDialog
      v-model="installOpen"
      :item="installTarget"
      :saving="mutating"
      :error="formError"
      @save="onInstall"
    />
  </div>
</template>

<style scoped lang="scss">
.catalogue-search {
  min-width: 200px;
}
</style>
