<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import {
  mdiAlertCircle,
  mdiContentCopy,
  mdiFilterVariant,
  mdiRadar,
  mdiRefresh,
  mdiReplay,
  mdiTextBoxOutline,
} from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppCard from "@/components/cards/AppCard.vue";
import EventTypeChip from "@/features/events/components/EventTypeChip.vue";
import EventDetailDrawer from "@/features/events/components/EventDetailDrawer.vue";
import DateRangeField from "@/features/events/components/DateRangeField.vue";
import ReplayToEndpointDialog from "@/features/events/components/ReplayToEndpointDialog.vue";
import { useEventsList } from "@/features/events/composables/useEventsList";
import { useEventDetail } from "@/features/events/composables/useEventDetail";
import { EVENT_TYPES } from "@/features/events/helpers/types";
import { accountLabelOf } from "@/features/events/helpers/accountCache";
import { formatDateTime } from "@/helpers/Date";
import { actionOf } from "@/constants/Actions";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const { t } = useI18n();
const { mdAndUp } = useDisplay();
const retryAction = actionOf("retry");
const copyAction = actionOf("copy");
const replayAction = actionOf("replay");
const filtersOpen = ref(false);
const replayOpen = ref(false);

const {
  items,
  total,
  page,
  ipp,
  sortBy,
  filterType,
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
  highlighted,
  stream,
  load,
  loadAccounts,
  clearFilters,
  toggleLive,
} = useEventsList();

const {
  open: detailOpen,
  detail,
  jsonText,
  loadError: detailError,
  isFirstLoad: detailFirstLoad,
  isRefreshing: detailRefreshing,
  show: showDetail,
} = useEventDetail();

const headers = computed<ServerTableHeader[]>(() => [
  { key: "time", title: t("events.col_time"), sortable: true },
  { key: "type", title: t("events.col_type"), sortable: false },
  { key: "account", title: t("events.col_account"), sortable: false },
  { key: "subject", title: t("events.col_subject"), sortable: false },
  { key: "id", title: t("events.col_id"), sortable: false },
]);

const typeItems = computed(() => EVENT_TYPES.map((e) => ({ title: e, value: e })));

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
);

const tableItems = computed(() =>
  items.value.map((row) => ({
    ...row,
    account: accountLabelOf(row.ebaccount ?? ""),
    highlight: highlighted.value.has(row.id),
  })),
);

const liveChip = computed(() => {
  if (stream.error.value) return { color: "error", text: t("events.live_error") };
  if (stream.reconnecting.value) return { color: "warning", text: t("events.live_reconnecting") };
  if (stream.live.value) return { color: "success", text: t("events.live_on") };
  return { color: "default", text: t("events.live_off") };
});

const retryLive = async (): Promise<void> => {
  stream.stop();
  stream.error.value = null;
  await toggleLive();
};

onMounted(() => {
  void load();
  void loadAccounts();
});

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const id = String(row.item.id ?? "");
  if (id) void showDetail(id);
};

const onFilterChange = (): void => {
  page.value = 1;
  void load();
};

const copyId = async (id: string, e: Event): Promise<void> => {
  e.stopPropagation();
  try {
    await navigator.clipboard.writeText(id);
  } catch {
    /* ignore */
  }
};
</script>

<template>
  <div>
    <div class="mb-6 d-flex flex-wrap align-center justify-space-between ga-3">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("events.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("events.subtitle") }}
        </p>
      </div>
      <div class="d-flex flex-wrap align-center ga-2">
        <v-chip
          :color="liveChip.color"
          label
        >
          {{ liveChip.text }}
        </v-chip>
        <v-btn
          :color="replayAction.color"
          :prepend-icon="mdiReplay"
          @click="replayOpen = true"
        >
          {{ t("events.replay_action") }}
        </v-btn>
        <v-btn
          :color="stream.live.value ? 'warning' : 'success'"
          :prepend-icon="mdiRadar"
          @click="toggleLive"
        >
          {{ stream.live.value ? t("events.live_stop") : t("events.live_start") }}
        </v-btn>
      </div>
    </div>

    <ReplayToEndpointDialog
      v-model="replayOpen"
      :initial-from="filterFrom"
      :initial-to="filterTo"
      :initial-type="filterType || undefined"
    />

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
        {{ t("events.filters") }}
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
          :title="t('events.filters')"
          as="sheet"
        >
          <AutocompleteSearch
            v-model="filterType"
            :items="typeItems"
            :label="t('events.filter_type')"
            class="no-validation mb-3"
            clearable
            hide-details
            @update:model-value="onFilterChange"
          />
          <AutocompleteSearch
            v-model="filterAccountId"
            :items="accountItems"
            :loading="accountsLoading"
            :label="t('events.filter_account')"
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
            :label="t('events.filter_dates')"
            class="mb-3"
            @change="onFilterChange"
          />

          <template #footer>
            <v-btn
              v-if="activeFilterCount"
              @click="clearFilters"
            >
              {{ t("events.clear_filters") }}
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
          v-model="filterType"
          :items="typeItems"
          :label="t('events.filter_type')"
          class="no-validation"
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
          v-model="filterAccountId"
          :items="accountItems"
          :loading="accountsLoading"
          :label="t('events.filter_account')"
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
          :label="t('events.filter_dates')"
          @change="onFilterChange"
        />
      </v-col>
    </v-row>

    <v-alert
      v-if="stream.error.value"
      type="error"
      class="mb-4"
      data-testid="events-live-error"
    >
      {{ t("events.live_error") }}: {{ stream.error.value }}
      <template #append>
        <v-btn
          :color="retryAction.color"
          :prepend-icon="mdiRefresh"
          @click="retryLive"
        >
          {{ t("events.retry") }}
        </v-btn>
      </template>
    </v-alert>

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
          {{ t("events.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <AppCard
      v-else-if="empty"
      class="mb-4"
    >
      <div class="d-flex flex-column align-center text-center pa-6 ga-3">
        <v-avatar-hero
          color="info"
        >
          <v-icon
            :icon="mdiTextBoxOutline"
          />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("events.empty_title") }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("events.empty_body") }}
        </p>
      </div>
    </AppCard>

    <TableCard
      v-else
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      :headers="headers"
      :items="tableItems"
      :total="total"
      :loading="isFirstLoad || isRefreshing"
      :searchable="false"
      table-name="events"
      item-value="id"
      @click:row="onRowClick"
    >
      <template #no-data>
        <div class="text-center pa-4">
          <div class="text-body-medium mb-2">
            {{ noResults ? t("events.no_results") : t("tables.no_results") }}
          </div>
          <v-btn
            v-if="noResults"
            color="primary"
            @click="clearFilters"
          >
            {{ t("events.clear_filters") }}
          </v-btn>
        </div>
      </template>

      <template #[`item.time`]="{ item }">
        <span :class="{ 'events-row--flash': item.highlight }">
          {{ formatDateTime(String(item.time || "")) }}
        </span>
      </template>

      <template #[`item.type`]="{ item }">
        <EventTypeChip :type="String(item.type || '')" />
      </template>

      <template #[`item.account`]="{ item }">
        {{ item.account || "—" }}
      </template>

      <template #[`item.subject`]="{ item }">
        <span class="font-family-monospace text-body-small">
          {{ item.subject }}
        </span>
      </template>

      <template #[`item.id`]="{ item }">
        <v-btn-affix
          v-tippy="String(item.id)"
          :icon="mdiContentCopy"
          :color="copyAction.color"
          :aria-label="t('events.copy_id')"
          @click="copyId(String(item.id), $event)"
        />
      </template>
    </TableCard>

    <EventDetailDrawer
      v-model="detailOpen"
      :detail="detail"
      :json-text="jsonText"
      :load-error="detailError"
      :is-first-load="detailFirstLoad"
      :is-refreshing="detailRefreshing"
    />
  </div>
</template>

<style scoped lang="scss">
.events-row--flash {
  animation: events-flash 1.6s ease;
}

@keyframes events-flash {
  from {
    background: rgb(var(--v-theme-success), 0.28);
  }

  to {
    background: transparent;
  }
}
</style>
