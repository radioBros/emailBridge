<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiAlertCircle, mdiFilterVariant, mdiLan, mdiRefresh, mdiReplay } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppCard from "@/components/cards/AppCard.vue";
import DeliveryStateChip from "@/features/deliveries/components/DeliveryStateChip.vue";
import DeliveryDetailDrawer from "@/features/deliveries/components/DeliveryDetailDrawer.vue";
import DateRangeField from "@/features/deliveries/components/DateRangeField.vue";
import ReplayMatchingDialog from "@/features/deliveries/components/ReplayMatchingDialog.vue";
import { useDeliveriesList } from "@/features/deliveries/composables/useDeliveriesList";
import { useDeliveryDetail } from "@/features/deliveries/composables/useDeliveryDetail";
import {
  DELIVERY_STATE_KEYS,
  lastStatusCode,
} from "@/features/deliveries/helpers/states";
import {
  accountLabelOf,
  endpointLabelOf,
  routeLabelOf,
} from "@/features/deliveries/helpers/labelCache";
import { WEBHOOK_EVENT_TYPES } from "@/features/webhooks/eventTypes";
import { formatDateTime } from "@/helpers/Date";
import { actionOf } from "@/constants/Actions";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const { t, te } = useI18n();
const { mdAndUp } = useDisplay();
const retryAction = actionOf("retry");
const replayAction = actionOf("replay");
const filtersOpen = ref(false);
const matchingOpen = ref(false);

const {
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  filterAccountId,
  filterEndpointId,
  filterEventType,
  filterState,
  filterFrom,
  filterTo,
  failedQuick,
  selected,
  replaying,
  accountOptions,
  endpointOptions,
  accountsLoading,
  endpointsLoading,
  loadError,
  empty,
  noResults,
  activeFilterCount,
  isFirstLoad,
  isRefreshing,
  load,
  loadAccounts,
  loadEndpoints,
  clearFilters,
  setFailedQuick,
  replayOne,
  replaySelected,
  replayMatching,
} = useDeliveriesList();

const {
  open: detailOpen,
  detail,
  loadError: detailError,
  canReplay,
  isFirstLoad: detailFirstLoad,
  isRefreshing: detailRefreshing,
  show: showDetail,
} = useDeliveryDetail();

const headers = computed<ServerTableHeader[]>(() => [
  { key: "select", title: "", sortable: false },
  { key: "createdAt", title: t("deliveries.col_created"), sortable: true },
  { key: "eventType", title: t("deliveries.col_event"), sortable: false },
  { key: "endpointId", title: t("deliveries.col_endpoint"), sortable: false },
  { key: "routeId", title: t("deliveries.col_route"), sortable: false },
  { key: "accountId", title: t("deliveries.col_account"), sortable: false },
  { key: "state", title: t("deliveries.col_state"), sortable: true },
  { key: "attempts", title: t("deliveries.col_attempts"), sortable: false },
  { key: "lastStatus", title: t("deliveries.col_status"), sortable: false },
  { key: "actions", title: t("deliveries.col_actions"), sortable: false, align: "end" },
]);

const stateItems = computed(() =>
  DELIVERY_STATE_KEYS.map((s) => ({
    title: te(`deliveries.state_${s}`) ? t(`deliveries.state_${s}`) : s,
    value: s,
  })),
);

const eventTypeItems = computed(() =>
  WEBHOOK_EVENT_TYPES.map((e) => ({ title: e, value: e })),
);

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
);

const endpointItems = computed(() =>
  endpointOptions.value.map((e) => ({
    title: e.name,
    value: e.id,
  })),
);

const tableItems = computed(() =>
  items.value.map((row) => ({
    ...row,
    attempts: row.attempts?.length ?? 0,
    lastStatus: lastStatusCode(row.attempts),
    accountName: accountLabelOf(row.accountId ?? ""),
    endpointName: endpointLabelOf(row.endpointId),
    routeName: row.routeId ? routeLabelOf(row.routeId) : "—",
  })),
);

const allSelectedOnPage = computed({
  get: () =>
    items.value.length > 0 && items.value.every((r) => selected.value.includes(r.id)),
  set: (on: boolean) => {
    if (on) {
      selected.value = [...new Set([...selected.value, ...items.value.map((r) => r.id)])];
    } else {
      const pageIds = new Set(items.value.map((r) => r.id));
      selected.value = selected.value.filter((id) => !pageIds.has(id));
    }
  },
});

onMounted(() => {
  void load();
  void loadAccounts();
  void loadEndpoints();
});

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const id = String(row.item.id ?? "");
  if (id) void showDetail(id);
};

const onFilterChange = (): void => {
  page.value = 1;
  void load();
};

const toggleSelect = (id: string, on: boolean): void => {
  if (on) {
    if (!selected.value.includes(id)) selected.value = [...selected.value, id];
  } else {
    selected.value = selected.value.filter((x) => x !== id);
  }
};

const onReplaySelected = async (): Promise<void> => {
  await replaySelected();
};

const onReplayMatching = (): void => {
  matchingOpen.value = true;
};

const onConfirmMatching = async (includeReplayed: boolean): Promise<void> => {
  const ok = await replayMatching(includeReplayed);
  if (ok) matchingOpen.value = false;
};

const onReplayDetail = async (): Promise<void> => {
  const id = detail.value?.id;
  if (!id) return;
  const ok = await replayOne(id);
  if (ok) detailOpen.value = false;
};
</script>

<template>
  <div>
    <div class="mb-6 d-flex flex-wrap align-center justify-space-between ga-3">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("deliveries.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("deliveries.subtitle") }}
        </p>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-chip
          :color="failedQuick ? 'error' : 'default'"
          class="align-self-center"
          @click="setFailedQuick(!failedQuick)"
        >
          {{ t("deliveries.failed_quick") }}
        </v-chip>
        <v-btn
          :color="replayAction.color"
          :prepend-icon="mdiReplay"
          :disabled="!selected.length"
          :loading="replaying"
          @click="onReplaySelected"
        >
          {{ t("deliveries.replay_selected") }}
        </v-btn>
        <v-btn
          :color="replayAction.color"
          :prepend-icon="replayAction.icon"
          :loading="replaying"
          @click="onReplayMatching"
        >
          {{ t("deliveries.replay_matching") }}
        </v-btn>
      </div>
    </div>

    <ReplayMatchingDialog
      v-model="matchingOpen"
      :total="total"
      :loading="replaying"
      @confirm="onConfirmMatching"
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
        {{ t("deliveries.filters") }}
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
          :title="t('deliveries.filters')"
          as="sheet"
        >
          <AutocompleteSearch
            v-model="filterEndpointId"
            :items="endpointItems"
            :loading="endpointsLoading"
            :label="t('deliveries.filter_endpoint')"
            class="no-validation mb-3"
            server-search
            clearable
            hide-details
            @update:search="loadEndpoints"
            @update:model-value="onFilterChange"
          />
          <AutocompleteSearch
            v-model="filterState"
            :items="stateItems"
            :label="t('deliveries.filter_state')"
            class="no-validation mb-3"
            clearable
            hide-details
            @update:model-value="onFilterChange"
          />
          <AutocompleteSearch
            v-model="filterEventType"
            :items="eventTypeItems"
            :label="t('deliveries.filter_event')"
            class="no-validation mb-3"
            clearable
            hide-details
            @update:model-value="onFilterChange"
          />
          <AutocompleteSearch
            v-model="filterAccountId"
            :items="accountItems"
            :loading="accountsLoading"
            :label="t('deliveries.filter_account')"
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
            :label="t('deliveries.filter_dates')"
            class="mb-3"
            @change="onFilterChange"
          />

          <template #footer>
            <v-btn
              v-if="activeFilterCount"
              @click="clearFilters"
            >
              {{ t("deliveries.clear_filters") }}
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
          v-model="filterEndpointId"
          :items="endpointItems"
          :loading="endpointsLoading"
          :label="t('deliveries.filter_endpoint')"
          class="no-validation table-filter"
          server-search
          clearable
          hide-details
          @update:search="loadEndpoints"
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        cols="12"
        md="6"
        lg
      >
        <AutocompleteSearch
          v-model="filterState"
          :items="stateItems"
          :label="t('deliveries.filter_state')"
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
          v-model="filterEventType"
          :items="eventTypeItems"
          :label="t('deliveries.filter_event')"
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
          v-model="filterAccountId"
          :items="accountItems"
          :loading="accountsLoading"
          :label="t('deliveries.filter_account')"
          class="no-validation table-filter"
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
          :label="t('deliveries.filter_dates')"
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
          {{ t("deliveries.retry") }}
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
            :icon="mdiLan"
          />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("deliveries.empty_title") }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("deliveries.empty_body") }}
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
      table-name="deliveries"
      item-value="id"
      searchable
      @click:row="onRowClick"
    >
      <template #no-data>
        <div class="text-center pa-4">
          <div class="text-body-medium mb-2">
            {{ noResults ? t("deliveries.no_results") : t("tables.no_results") }}
          </div>
          <v-btn
            v-if="noResults"
            color="primary"
            @click="clearFilters"
          >
            {{ t("deliveries.clear_filters") }}
          </v-btn>
        </div>
      </template>
      <template #[`header.select`]>
        <v-checkbox
          v-model="allSelectedOnPage"
          :aria-label="t('deliveries.select_all')"
          class="no-validation ma-0 pa-0"
          hide-details
          @click.stop
        />
      </template>
      <template #[`item.select`]="{ item }">
        <v-checkbox
          :model-value="selected.includes(String(item.id))"
          :aria-label="t('deliveries.select_row')"
          class="no-validation ma-0 pa-0"
          hide-details
          @click.stop
          @update:model-value="toggleSelect(String(item.id), Boolean($event))"
        />
      </template>
      <template #[`item.createdAt`]="{ item }">
        {{ formatDateTime(item.createdAt) }}
      </template>
      <template #[`item.endpointId`]="{ item }">
        {{ item.endpointName }}
      </template>
      <template #[`item.routeId`]="{ item }">
        {{ item.routeName }}
      </template>
      <template #[`item.accountId`]="{ item }">
        {{ item.accountName || "—" }}
      </template>
      <template #[`item.state`]="{ item }">
        <div class="d-flex flex-wrap align-center ga-1">
          <DeliveryStateChip :state="String(item.state)" />
          <v-chip
            v-if="item.replayedAt"
            color="secondary"
            label
          >
            {{ t("deliveries.chip_replayed") }}
          </v-chip>
        </div>
      </template>
      <template #[`item.lastStatus`]="{ item }">
        {{ item.lastStatus ?? "—" }}
      </template>
      <template #[`item.actions`]="{ item }">
        <v-btn
          :color="replayAction.color"
          :icon="replayAction.icon"
          :aria-label="t('deliveries.replay')"
          :loading="replaying"
          @click.stop="replayOne(String(item.id))"
        />
      </template>
    </TableCard>

    <DeliveryDetailDrawer
      v-model="detailOpen"
      :detail="detail"
      :load-error="detailError"
      :is-first-load="detailFirstLoad"
      :is-refreshing="detailRefreshing"
      :can-replay="canReplay"
      :replaying="replaying"
      @replay="onReplayDetail"
    />
  </div>
</template>
