<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import {
  mdiHarddisk,
  mdiShieldLock,
  mdiTextBoxOutline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import StatusChip from "@/components/common/StatusChip.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { LogLevels } from "@/constants/Statuses";
import { useLogs } from "@/features/logs/composables/useLogs";
import { formatBytes, formatLogTime } from "@/features/logs/helpers/logFormat";
import { accountLabelOf } from "@/features/logs/helpers/accountCache";
import DateRangeField from "@/features/logs/components/DateRangeField.vue";
import LogDetailDrawer from "@/features/logs/components/LogDetailDrawer.vue";
import LogSettingsDialog from "@/features/logs/components/LogSettingsDialog.vue";
import type { LogRecord } from "@/api/logs";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const { t } = useI18n();
const { mdAndUp } = useDisplay();
const exportAction = actionOf("export");
const refreshAction = actionOf("refresh");
const liveAction = actionOf("live");
const liveActiveAction = actionOf("liveActive");
const settingsAction = actionOf("settings");
const filterAction = actionOf("filter");
const idsAction = actionOf("ids");
const closeAction = actionOf("close");
const filtersOpen = ref(false);
const moreIdsOpen = ref(false);

const {
  from,
  to,
  categories,
  level,
  accountId,
  requestId,
  eventId,
  deliveryId,
  outboundId,
  traceId,
  search,
  page,
  ipp,
  sortBy,
  items,
  total,
  diskCards,
  loadError,
  forbidden,
  selected,
  drawerOpen,
  settingsOpen,
  logSettings,
  settingsSaving,
  exporting,
  accountOptions,
  accountsLoading,
  categoryItems,
  levelItems,
  idFilterCount,
  activeFilterCount,
  isFirstLoad,
  isRefreshing,
  canWriteLogs,
  tail,
  load,
  onFilterChange,
  setPage,
  setIpp,
  setSearch,
  openDetail,
  toggleLive,
  doExport,
  openSettings,
  saveSettings,
  loadAccounts,
  clearIdFilters,
  clearFilters,
} = useLogs();

onMounted(() => {
  void load();
  void loadAccounts("");
});

const retryLive = async (): Promise<void> => {
  tail.stop();
  tail.error.value = null;
  await toggleLive();
};

const headers = computed<ServerTableHeader[]>(() => [
  { key: "time", title: t("logs.col_time"), sortable: false, forced: true },
  { key: "level", title: t("logs.col_level"), sortable: false },
  { key: "category", title: t("logs.col_category"), sortable: false },
  { key: "msg", title: t("logs.col_message"), sortable: false, forced: true },
  { key: "accountId", title: t("logs.col_account"), sortable: false },
  { key: "requestId", title: t("logs.col_request"), sortable: false },
]);

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({
    title: a.email ? `${a.name} · ${a.email}` : a.name,
    value: a.id,
  })),
);

const tableItems = computed(() =>
  items.value.map((row) => {
    const acc = row.accountId ? accountLabelOf(row.accountId) : null;
    return {
      ...row,
      accountName: acc?.name || row.accountId || "—",
    };
  }) as unknown as Record<string, unknown>[],
);

const onRowClick = (_e: Event, row: { item: Record<string, unknown> }): void => {
  openDetail(row.item as unknown as LogRecord);
};

const asLog = (item: unknown): LogRecord & { accountName?: string } =>
  item as LogRecord & { accountName?: string };
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-start justify-space-between ga-3 mb-6">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("logs.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("logs.subtitle") }}
        </p>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-btn
          :color="tail.live.value ? liveActiveAction.color : liveAction.color"
          :prepend-icon="tail.live.value ? liveActiveAction.icon : liveAction.icon"
          @click="toggleLive"
        >
          {{ tail.live.value ? t("logs.live_stop") : t("logs.live_start") }}
        </v-btn>
        <v-btn
          :color="exportAction.color"
          :prepend-icon="exportAction.icon"
          :loading="exporting"
          @click="doExport"
        >
          {{ t("logs.export") }}
        </v-btn>
        <v-btn
          :color="settingsAction.color"
          :prepend-icon="settingsAction.icon"
          @click="openSettings"
        >
          {{ t("logs.category_settings") }}
        </v-btn>
      </div>
    </div>

    <AppCard
      v-if="forbidden"
      class="text-center py-8"
    >
      <v-avatar-hero
        color="warning"
        class="mb-4"
      >
        <v-icon
          :icon="mdiShieldLock"
        />
      </v-avatar-hero>
      <div class="text-title-medium mb-1">
        {{ t("logs.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("logs.no_permission_body") }}
      </p>
    </AppCard>

    <template v-else>
      <v-alert
        v-if="tail.error.value"
        type="error"
        class="mb-4"
        data-testid="logs-live-error"
      >
        {{ t("logs.live_error") }}: {{ tail.error.value }}
        <template #append>
          <v-btn
            :color="refreshAction.color"
            @click="retryLive"
          >
            {{ t("logs.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="refreshAction.color"
            @click="load"
          >
            {{ t("logs.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <v-row
        v-if="diskCards.length"
        class="mb-4"
      >
        <v-col
          v-for="card in diskCards"
          :key="card.category"
          cols="6"
          sm="4"
          md="3"
          lg="2"
        >
          <AppCard
            content-class="pa-3"
            slim
          >
            <div class="d-flex align-center ga-2 mb-1">
              <v-icon-inline
                :icon="mdiHarddisk"
                color="secondary"
              />
              <span class="text-label-large text-medium-emphasis">
                {{ card.category }}
              </span>
            </div>
            <div class="text-title-medium font-weight-bold text-numeric">
              {{ formatBytes(card.bytes) }}
            </div>
          </AppCard>
        </v-col>
      </v-row>

      <!-- Mobile: Filters button + bottom sheet -->
      <div
        v-if="!mdAndUp"
        class="mb-4"
      >
        <v-btn
          :prepend-icon="filterAction.icon"
          :color="filterAction.color"
          block
          @click="filtersOpen = true"
        >
          {{ t("logs.filters") }}
          <v-badge
            v-if="activeFilterCount"
            :content="activeFilterCount"
            :color="filterAction.color"
            class="ms-2"
            inline
          />
        </v-btn>
        <v-bottom-sheet v-model="filtersOpen">
          <AppDrawerPanel
            v-model="filtersOpen"
            :title="t('logs.filters')"
            as="sheet"
          >
            <DateRangeField
              v-model:from="from"
              v-model:to="to"
              :label="t('logs.filter_dates')"
              class="mb-3"
              @change="onFilterChange"
            />
            <AutocompleteSearch
              v-model="level"
              :items="levelItems"
              :label="t('logs.filter_level')"
              class="no-validation mb-3"
              clearable
              hide-details
              @update:model-value="onFilterChange"
            />
            <AutocompleteSearch
              v-model="categories"
              :items="categoryItems"
              :label="t('logs.filter_category')"
              class="no-validation mb-3"
              multiple
              chips
              hide-details
              @update:model-value="onFilterChange"
            />
            <AutocompleteSearch
              v-model="accountId"
              :items="accountItems"
              :loading="accountsLoading"
              :label="t('logs.filter_account')"
              class="no-validation mb-3"
              clearable
              server-search
              hide-details
              @update:search="loadAccounts"
              @update:model-value="onFilterChange"
            />
            <div class="text-label-large text-medium-emphasis mb-2">
              {{ t("logs.more_filters") }}
              <v-badge
                v-if="idFilterCount"
                :content="idFilterCount"
                color="primary"
                class="ms-1"
                inline
              />
            </div>
            <v-text-field
              v-model="requestId"
              :label="t('logs.filter_request')"
              class="no-validation mb-3"
              hide-details
              @change="onFilterChange"
            />
            <v-text-field
              v-model="eventId"
              :label="t('logs.filter_event')"
              class="no-validation mb-3"
              hide-details
              @change="onFilterChange"
            />
            <v-text-field
              v-model="deliveryId"
              :label="t('logs.filter_delivery')"
              class="no-validation mb-3"
              hide-details
              @change="onFilterChange"
            />
            <v-text-field
              v-model="outboundId"
              :label="t('logs.filter_outbound')"
              class="no-validation mb-3"
              hide-details
              @change="onFilterChange"
            />
            <v-text-field
              v-model="traceId"
              :label="t('logs.filter_trace')"
              class="no-validation mb-3"
              hide-details
              @change="onFilterChange"
            />

            <template #footer>
              <v-btn
                v-if="activeFilterCount"
                :color="closeAction.color"
                @click="clearFilters"
              >
                {{ t("logs.clear_filters") }}
              </v-btn>
              <v-btn
                :color="closeAction.color"
                @click="filtersOpen = false"
              >
                {{ t("logs.close") }}
              </v-btn>
            </template>
          </AppDrawerPanel>
        </v-bottom-sheet>
      </div>

      <!-- Desktop: primary filters row -->
      <v-row
        v-else
        class="mb-2"
      >
        <v-col
          cols="12"
          md="auto"
        >
          <DateRangeField
            v-model:from="from"
            v-model:to="to"
            :label="t('logs.filter_dates')"
            @change="onFilterChange"
          />
        </v-col>
        <v-col
          cols="12"
          md="6"
          lg
        >
          <AutocompleteSearch
            v-model="level"
            :items="levelItems"
            :label="t('logs.filter_level')"
            class="no-validation table-filter"
            clearable
            hide-details
            @update:model-value="onFilterChange"
          />
        </v-col>
        <v-col
          cols="12"
          md="6"
          lg
        >
          <AutocompleteSearch
            v-model="categories"
            :items="categoryItems"
            :label="t('logs.filter_category')"
            class="no-validation table-filter"
            multiple
            chips
            hide-details
            @update:model-value="onFilterChange"
          />
        </v-col>
        <v-col
          cols="12"
          md="6"
          lg
        >
          <AutocompleteSearch
            v-model="accountId"
            :items="accountItems"
            :loading="accountsLoading"
            :label="t('logs.filter_account')"
            class="no-validation table-filter"
            clearable
            server-search
            hide-details
            @update:search="loadAccounts"
            @update:model-value="onFilterChange"
          />
        </v-col>
        <v-col
          cols="12"
          md="auto"
          class="d-flex align-center"
        >
          <v-btn
            :prepend-icon="idsAction.icon"
            :color="idsAction.color"
            block
            @click="moreIdsOpen = !moreIdsOpen"
          >
            <v-badge
              v-if="idFilterCount"
              :content="idFilterCount"
              :color="idsAction.color"
              inline
            >
              {{ t("logs.more_short") }}
            </v-badge>
            <template v-else>
              {{ t("logs.more_short") }}
            </template>
          </v-btn>
        </v-col>
      </v-row>

      <v-expand-transition>
        <v-row
          v-if="mdAndUp && moreIdsOpen"
          class="mb-4"
        >
          <v-col
            cols="12"
            md="2"
          >
            <v-text-field
              v-model="requestId"
              :label="t('logs.filter_request')"
              class="no-validation"
              hide-details
              @change="onFilterChange"
            />
          </v-col>
          <v-col
            cols="12"
            md="2"
          >
            <v-text-field
              v-model="eventId"
              :label="t('logs.filter_event')"
              class="no-validation"
              hide-details
              @change="onFilterChange"
            />
          </v-col>
          <v-col
            cols="12"
            md="2"
          >
            <v-text-field
              v-model="deliveryId"
              :label="t('logs.filter_delivery')"
              class="no-validation"
              hide-details
              @change="onFilterChange"
            />
          </v-col>
          <v-col
            cols="12"
            md="2"
          >
            <v-text-field
              v-model="outboundId"
              :label="t('logs.filter_outbound')"
              class="no-validation"
              hide-details
              @change="onFilterChange"
            />
          </v-col>
          <v-col
            cols="12"
            md="2"
          >
            <v-text-field
              v-model="traceId"
              :label="t('logs.filter_trace')"
              class="no-validation"
              hide-details
              @change="onFilterChange"
            />
          </v-col>
          <v-col
            cols="12"
            md="2"
            class="d-flex align-center"
          >
            <v-btn
              v-if="idFilterCount"
              :color="closeAction.color"
              @click="clearIdFilters"
            >
              {{ t("logs.clear_id_filters") }}
            </v-btn>
          </v-col>
        </v-row>
      </v-expand-transition>

      <div
        @mouseenter="tail.live.value && tail.setPaused(true)"
        @mouseleave="tail.setPaused(false)"
      >
        <TableCard
          :headers="headers"
          :items="tableItems"
          :total="total"
          :page="page"
          :ipp="ipp"
          :sort-by="sortBy"
          :loading="isFirstLoad || isRefreshing"
          :search="search"
          table-name="logs"
          item-value="id"
          searchable
          @update:page="setPage"
          @update:ipp="setIpp"
          @update:search="setSearch"
          @click:row="onRowClick"
        >
          <!-- eslint-disable vue/valid-v-slot -- TableCard column slots use item.<key> -->
          <template #item.time="{ item }">
            <span class="text-numeric text-body-small">
              {{ formatLogTime(asLog(item).time) }}
            </span>
          </template>

          <template #item.level="{ item }">
            <StatusChip
              :color="LogLevels[asLog(item).level].color"
              :icon="LogLevels[asLog(item).level].icon"
              :label="t(`logs.level.${asLog(item).level}`)"
            />
          </template>

          <template #item.category="{ item }">
            <v-chip
              label
            >
              {{ asLog(item).category }}
            </v-chip>
          </template>

          <template #item.msg="{ item }">
            <span class="text-body-medium text-truncate d-inline-block max-msg">
              {{ asLog(item).msg }}
            </span>
          </template>

          <template #item.accountId="{ item }">
            <span class="text-body-small font-weight-medium">
              {{ asLog(item).accountName || "—" }}
            </span>
          </template>

          <template #item.requestId="{ item }">
            <span class="text-body-small text-medium-emphasis text-truncate d-inline-block max-id">
              {{ asLog(item).requestId || "—" }}
            </span>
          </template>
          <!-- eslint-enable vue/valid-v-slot -->

          <template #no-data>
            <div class="text-center py-10">
              <v-avatar-hero
                color="info"
                class="mb-4"
              >
                <v-icon
                  :icon="mdiTextBoxOutline"
                />
              </v-avatar-hero>
              <div class="text-title-medium mb-1">
                {{ t("logs.empty_title") }}
              </div>
              <p class="text-body-medium text-medium-emphasis mb-0">
                {{ t("logs.empty_body") }}
              </p>
            </div>
          </template>
        </TableCard>
      </div>
    </template>

    <LogDetailDrawer
      v-model="drawerOpen"
      :record="selected"
    />

    <LogSettingsDialog
      v-model="settingsOpen"
      :settings="logSettings"
      :saving="settingsSaving"
      :can-write="canWriteLogs"
      @update:settings="logSettings = $event"
      @save="saveSettings"
    />
  </div>
</template>

<style lang="scss" scoped>
.max-msg {
  max-width: 28rem;
}

.max-id {
  max-width: 10rem;
}

</style>
