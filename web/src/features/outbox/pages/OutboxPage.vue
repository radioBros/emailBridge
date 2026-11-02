<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { useDisplay } from "vuetify";
import { mdiAlertCircle, mdiInboxArrowUp, mdiPencilPlus, mdiRefresh } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppCard from "@/components/cards/AppCard.vue";
import OutboundStateChip from "@/features/outbox/components/OutboundStateChip.vue";
import OutboxDetailDrawer from "@/features/outbox/components/OutboxDetailDrawer.vue";
import DateRangeField from "@/features/outbox/components/DateRangeField.vue";
import ComposeDialog from "@/features/outbox/components/ComposeDialog.vue";
import { useOutboxList } from "@/features/outbox/composables/useOutboxList";
import { useOutboxDetail } from "@/features/outbox/composables/useOutboxDetail";
import {
  formatAttempts,
  formatRecipients,
  OUTBOUND_STATE_KEYS,
} from "@/features/outbox/helpers/outboundState";
import { accountLabelOf } from "@/features/outbox/helpers/accountCache";
import { formatDateTime } from "@/helpers/Date";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const { t, te } = useI18n();
const route = useRoute();
const router = useRouter();
const { mdAndUp } = useDisplay();
const confirm = useConfirm();
const retryAction = actionOf("retry");
const createAction = actionOf("create");
const filterAction = actionOf("filter");
const closeAction = actionOf("close");
const filtersOpen = ref(false);
const composeOpen = ref(false);

const {
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  filterAccountId,
  filterState,
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
  clearFilters,
} = useOutboxList();

const {
  open: detailOpen,
  detail,
  loadError: detailError,
  cancelling,
  canCancel,
  isFirstLoad: detailFirstLoad,
  isRefreshing: detailRefreshing,
  show: showDetail,
  cancel: cancelDetail,
} = useOutboxDetail();

const headers = computed<ServerTableHeader[]>(() => [
  { key: "createdAt", title: t("outbox.col_created"), sortable: true },
  { key: "accountId", title: t("outbox.col_account"), sortable: false },
  { key: "to", title: t("outbox.col_to"), sortable: false },
  { key: "subject", title: t("outbox.col_subject"), sortable: false },
  { key: "state", title: t("outbox.col_state"), sortable: true },
  { key: "attempts", title: t("outbox.col_attempts"), sortable: false },
  { key: "nextAttemptAt", title: t("outbox.col_next"), sortable: false },
]);

const stateItems = computed(() => [
  { title: t("outbox.filter_all_states"), value: "" },
  ...OUTBOUND_STATE_KEYS.map((s) => ({
    title: te(`outbox.state_${s}`) ? t(`outbox.state_${s}`) : s,
    value: s,
  })),
]);

const accountItems = computed(() => [
  { title: t("outbox.filter_all_accounts"), value: "" },
  ...accountOptions.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
]);

const tableItems = computed(() =>
  items.value.map((row) => {
    const acc = accountLabelOf(row.accountId);
    return {
      ...row,
      to: formatRecipients(row.envelope?.to),
      attempts: formatAttempts(row.attempts?.length, row.maxAttempts),
      accountName: acc.name,
      accountEmail: acc.email,
    };
  }),
);

const openFromQuery = (): void => {
  const id = typeof route.query.id === "string" ? route.query.id : "";
  if (id) void showDetail(id);
};

onMounted(() => {
  void load();
  void loadAccounts();
  openFromQuery();
});

watch(
  () => route.query.id,
  () => {
    openFromQuery();
  },
);

watch(detailOpen, (v, was) => {
  // Only clear ?id= when the user closes an open drawer — not on the initial false.
  if (v || !was) return;
  if (route.query.id) {
    const next = { ...route.query };
    delete next.id;
    void router.replace({ query: next });
  }
});

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const id = String(row.item.id ?? "");
  if (id) {
    void router.replace({ query: { ...route.query, id } });
    void showDetail(id);
  }
};

const onFilterChange = (): void => {
  page.value = 1;
};

const onCancel = async (): Promise<void> => {
  const ok = await confirm({
    title: t("outbox.cancel_title"),
    body: t("outbox.cancel_body"),
    variant: "warning",
    confirmText: t("outbox.cancel"),
  });
  if (!ok) return;
  const cancelled = await cancelDetail();
  if (cancelled) void load();
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-start justify-space-between ga-3 mb-6">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("outbox.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("outbox.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="mdiPencilPlus"
        @click="composeOpen = true"
      >
        {{ t("outbox.compose.open") }}
      </v-btn>
    </div>

    <ComposeDialog
      v-model:open="composeOpen"
      @sent="load"
    />

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
        {{ t("outbox.filters") }}
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
          :title="t('outbox.filters')"
          as="sheet"
        >
          <AutocompleteSearch
            v-model="filterAccountId"
            :items="accountItems"
            :loading="accountsLoading"
            :label="t('outbox.filter_account')"
            class="no-validation mb-3"
            server-search
            clearable
            hide-details
            @update:search="loadAccounts"
            @update:model-value="onFilterChange"
          />
          <AutocompleteSearch
            v-model="filterState"
            :items="stateItems"
            :label="t('outbox.filter_state')"
            class="no-validation mb-3"
            clearable
            hide-details
            @update:model-value="onFilterChange"
          />
          <DateRangeField
            v-model:from="filterFrom"
            v-model:to="filterTo"
            :label="t('outbox.filter_dates')"
            class="mb-3"
            @change="onFilterChange"
          />

          <template #footer>
            <v-btn
              v-if="activeFilterCount"
              :color="closeAction.color"
              @click="clearFilters"
            >
              {{ t("outbox.clear_filters") }}
            </v-btn>
            <v-btn
              :color="closeAction.color"
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
      align="center"
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
          :label="t('outbox.filter_account')"
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
        md="6"
        lg
      >
        <AutocompleteSearch
          v-model="filterState"
          :items="stateItems"
          :label="t('outbox.filter_state')"
          class="no-validation table-filter"
          clearable
          hide-details
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
          :label="t('outbox.filter_dates')"
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
          {{ t("outbox.retry") }}
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
            :icon="mdiInboxArrowUp"
          />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("outbox.empty_title") }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("outbox.empty_body") }}
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
      table-name="outbox"
      item-value="id"
      searchable
      @click:row="onRowClick"
    >
      <template #no-data>
        <div class="text-center pa-4">
          <div class="text-body-medium mb-2">
            {{ noResults ? t("outbox.no_results") : t("tables.no_results") }}
          </div>
          <v-btn
            v-if="noResults"
            :color="filterAction.color"
            @click="clearFilters"
          >
            {{ t("outbox.clear_filters") }}
          </v-btn>
        </div>
      </template>
      <template #[`item.createdAt`]="{ item }">
        {{ formatDateTime(item.createdAt) }}
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
      <template #[`item.state`]="{ item }">
        <OutboundStateChip :state="String(item.state)" />
      </template>
      <template #[`item.nextAttemptAt`]="{ item }">
        {{ item.nextAttemptAt ? formatDateTime(item.nextAttemptAt) : "—" }}
      </template>
    </TableCard>

    <OutboxDetailDrawer
      v-model="detailOpen"
      :detail="detail"
      :load-error="detailError"
      :is-first-load="detailFirstLoad"
      :is-refreshing="detailRefreshing"
      :can-cancel="canCancel"
      :cancelling="cancelling"
      @cancel="onCancel"
    />
  </div>
</template>
