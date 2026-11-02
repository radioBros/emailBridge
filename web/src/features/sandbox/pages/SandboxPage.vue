<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiAlertCircle, mdiFilterVariant, mdiInbox, mdiRefresh } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppCard from "@/components/cards/AppCard.vue";
import SandboxViewerDrawer from "@/features/sandbox/components/SandboxViewerDrawer.vue";
import DateRangeField from "@/features/sandbox/components/DateRangeField.vue";
import { useSandboxList } from "@/features/sandbox/composables/useSandboxList";
import { useSandboxViewer } from "@/features/sandbox/composables/useSandboxViewer";
import { formatRecipients } from "@/features/sandbox/helpers/format";
import { accountLabelOf } from "@/features/sandbox/helpers/accountCache";
import { formatDateTime } from "@/helpers/Date";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const { t } = useI18n();
const { mdAndUp } = useDisplay();
const confirm = useConfirm();
const retryAction = actionOf("retry");
const filtersOpen = ref(false);

const {
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  filterAccountId,
  filterFrom,
  filterTo,
  accountOptions,
  accountsLoading,
  loadError,
  empty,
  noResults,
  activeFilterCount,
  isFirstLoad,
  isRefreshing,
  load,
  loadAccounts,
  removeLocal,
  clearFilters,
} = useSandboxList();

const {
  open: viewerOpen,
  detail,
  parsed,
  rawText,
  rawMissing,
  loadError: viewerError,
  loadImages,
  deleting,
  downloading,
  isFirstLoad: viewerFirstLoad,
  isRefreshing: viewerRefreshing,
  show: showViewer,
  download,
  remove,
} = useSandboxViewer();

const headers = computed<ServerTableHeader[]>(() => [
  { key: "capturedAt", title: t("sandbox.col_captured"), sortable: true },
  { key: "accountId", title: t("sandbox.col_account"), sortable: false },
  { key: "to", title: t("sandbox.col_to"), sortable: false },
  { key: "subject", title: t("sandbox.col_subject"), sortable: false },
  { key: "policy", title: t("sandbox.col_policy"), sortable: false },
]);

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
);

const tableItems = computed(() =>
  items.value.map((row) => {
    const acc = accountLabelOf(row.accountId);
    return {
      ...row,
      to: formatRecipients(row.envelope?.to),
      policy: t("sandbox.policy_capture"),
      accountName: acc.name,
      accountEmail: acc.email,
    };
  }),
);

onMounted(() => {
  void load();
  void loadAccounts();
});

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const id = String(row.item.id ?? "");
  if (id) void showViewer(id);
};

const onFilterChange = (): void => {
  page.value = 1;
};

const onDelete = async (): Promise<void> => {
  const id = detail.value?.id;
  const ok = await confirm({
    title: t("sandbox.delete_title"),
    body: t("sandbox.delete_body"),
    variant: "danger",
    confirmText: t("sandbox.delete"),
  });
  if (!ok || !id) return;
  const deleted = await remove();
  if (deleted) removeLocal(id);
};
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t("sandbox.title") }}
      </h1>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("sandbox.subtitle") }}
      </p>
    </div>

    <div
      v-if="!mdAndUp"
      class="mb-4"
    >
      <v-btn
        :prepend-icon="mdiFilterVariant"
        color="primary"
        block
        @click="filtersOpen = true"
      >
        {{ t("sandbox.filters") }}
        <v-badge
          v-if="activeFilterCount"
          :content="activeFilterCount"
          color="primary"
          class="ms-2"
          inline
        />
      </v-btn>
      <v-bottom-sheet v-model="filtersOpen">
        <AppDrawerPanel
          v-model="filtersOpen"
          :title="t('sandbox.filters')"
          as="sheet"
        >
          <AutocompleteSearch
            v-model="filterAccountId"
            :items="accountItems"
            :loading="accountsLoading"
            :label="t('sandbox.filter_account')"
            class="no-validation mb-3"
            server-search
            clearable
            hide-details
            @update:search="loadAccounts"
            @update:model-value="onFilterChange"
          />
          <DateRangeField
            v-model:from="filterFrom"
            v-model:to="filterTo"
            :label="t('sandbox.filter_dates')"
            class="mb-3"
            @change="onFilterChange"
          />

          <template #footer>
            <v-btn
              v-if="activeFilterCount"
              @click="clearFilters"
            >
              {{ t("sandbox.clear_filters") }}
            </v-btn>
            <v-btn
              color="primary"
              @click="filtersOpen = false"
            >
              {{ t("common.close") }}
            </v-btn>
          </template>
        </AppDrawerPanel>
      </v-bottom-sheet>
    </div>

    <v-row
      v-else
      class="mb-4"
    >
      <v-col
        cols="12"
        md="6"
        lg
      >
        <AutocompleteSearch
          v-model="filterAccountId"
          :items="accountItems"
          :loading="accountsLoading"
          :label="t('sandbox.filter_account')"
          class="no-validation"
          server-search
          clearable
          hide-details
          @update:search="loadAccounts"
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        cols="12"
        md="auto"
      >
        <DateRangeField
          v-model:from="filterFrom"
          v-model:to="filterTo"
          :label="t('sandbox.filter_dates')"
          @change="onFilterChange"
        />
      </v-col>
    </v-row>

    <AppCard
      v-if="loadError"
      class="mb-4"
    >
      <div class="d-flex align-center ga-4 pa-2">
        <v-avatar-lead
          color="error"
        >
          <v-icon
            :icon="mdiAlertCircle"
          />
        </v-avatar-lead>
        <div class="flex-grow-1">
          <div class="text-body-large">
            {{ loadError }}
          </div>
        </div>
        <v-btn
          :color="retryAction.color"
          :prepend-icon="mdiRefresh"
          @click="load"
        >
          {{ t("sandbox.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <AppCard
      v-else-if="empty"
      class="mb-4"
    >
      <div class="d-flex flex-column align-center text-center pa-6 ga-3">
        <v-avatar-hero
          color="accent"
        >
          <v-icon
            :icon="mdiInbox"
          />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("sandbox.empty_title") }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("sandbox.empty_body") }}
        </p>
      </div>
    </AppCard>

    <TableCard
      v-else
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      v-model:search="search"
      :headers="headers"
      :items="tableItems"
      :total="total"
      :loading="isFirstLoad || isRefreshing"
      table-name="sandbox"
      item-value="id"
      searchable
      @click:row="onRowClick"
    >
      <template #no-data>
        <div class="text-center pa-4">
          <div class="text-body-medium mb-2">
            {{ noResults ? t("sandbox.no_results") : t("tables.no_results") }}
          </div>
          <v-btn
            v-if="noResults"
            color="primary"
            @click="clearFilters"
          >
            {{ t("sandbox.clear_filters") }}
          </v-btn>
        </div>
      </template>
      <template #[`item.capturedAt`]="{ item }">
        {{ formatDateTime(item.capturedAt) }}
      </template>
      <template #[`item.accountId`]="{ item }">
        <div class="text-body-medium">
          {{ item.accountName || item.accountId }}
        </div>
        <div
          v-if="!mdAndUp && item.accountEmail"
          class="text-caption text-medium-emphasis"
        >
          {{ item.accountEmail }}
        </div>
      </template>
    </TableCard>

    <SandboxViewerDrawer
      v-model="viewerOpen"
      v-model:load-images="loadImages"
      :detail="detail"
      :parsed="parsed"
      :raw-text="rawText"
      :raw-missing="rawMissing"
      :load-error="viewerError"
      :is-first-load="viewerFirstLoad"
      :is-refreshing="viewerRefreshing"
      :deleting="deleting"
      :downloading="downloading"
      @download="download"
      @delete="onDelete"
    />
  </div>
</template>
