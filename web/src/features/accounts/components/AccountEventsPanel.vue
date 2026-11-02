<script setup lang="ts">
import { computed, onMounted, toRef, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiOpenInNew } from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import EventDetailDrawer from "@/features/events/components/EventDetailDrawer.vue";
import EventTypeChip from "@/features/events/components/EventTypeChip.vue";
import { useEventDetail } from "@/features/events/composables/useEventDetail";
import { EVENT_TYPES } from "@/features/events/helpers/types";
import { useAccountRecentEvents } from "@/features/accounts/composables/useAccountRecentEvents";

const props = defineProps<{ accountId: string }>();

const { t } = useI18n();
const router = useRouter();
const retryAction = actionOf("retry");

const accountId = toRef(props, "accountId");
const {
  items,
  total,
  page,
  ipp,
  sortBy,
  filterType,
  loadError,
  noResults,
  isLoading,
  load,
} = useAccountRecentEvents(accountId);

const {
  open: detailOpen,
  detail,
  jsonText,
  loadError: detailError,
  isFirstLoad: detailFirstLoad,
  isRefreshing: detailRefreshing,
  show: showDetail,
} = useEventDetail();

onMounted(() => {
  void load();
});

watch(accountId, () => {
  filterType.value = "";
  if (page.value !== 1) page.value = 1;
  else void load();
});

watch([page, ipp, sortBy], () => {
  void load();
});

watch(filterType, () => {
  if (page.value !== 1) page.value = 1;
  else void load();
});

const headers = computed<ServerTableHeader[]>(() => [
  { key: "time", title: t("accounts.events_col_time"), sortable: true },
  { key: "type", title: t("accounts.events_col_type"), sortable: false },
  { key: "subject", title: t("accounts.events_col_subject"), sortable: false },
  { key: "id", title: t("accounts.events_col_id"), sortable: false },
]);

const typeItems = computed(() => [
  { title: t("accounts.events_all_types"), value: "" },
  ...EVENT_TYPES.map((e) => ({ title: e, value: e })),
]);

const tableItems = computed(() => items.value.map((row) => ({ ...row })));

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const id = String(row.item.id ?? "");
  if (id) void showDetail(id);
};

const goAllEvents = (): void => {
  void router.push({
    path: "/events",
    query: { accountId: props.accountId },
  });
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-3 mb-4">
      <AutocompleteSearch
        v-model="filterType"
        :items="typeItems"
        :label="t('accounts.events_filter_type')"
        style="min-width: 220px; max-width: 360px"
        class="no-validation"
        clearable
        hide-details
      />
      <v-btn
        :prepend-icon="mdiOpenInNew"
        color="secondary"
        @click="goAllEvents"
      >
        {{ t("accounts.events_view_all") }}
      </v-btn>
    </div>

    <v-alert
      v-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
      <template #append>
        <v-btn
          :color="retryAction.color"
          @click="load"
        >
          {{ t("accounts.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <TableCard
      v-else
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      :headers="headers"
      :items="tableItems"
      :total="total"
      :loading="isLoading"
      :searchable="false"
      table-name="account-events"
      item-value="id"
      @click:row="onRowClick"
    >
      <template #[`item.time`]="{ item }">
        {{ formatDateTime(item.time) }}
      </template>
      <template #[`item.type`]="{ item }">
        <EventTypeChip :type="String(item.type)" />
      </template>
      <template #[`item.subject`]="{ item }">
        {{ item.subject || "—" }}
      </template>
      <template #[`item.id`]="{ item }">
        <span class="text-caption font-mono text-medium-emphasis">
          {{ item.id }}
        </span>
      </template>
      <template #no-data>
        <div class="text-center py-6 text-medium-emphasis">
          {{ noResults ? t("accounts.events_empty_filtered") : t("accounts.events_empty") }}
        </div>
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
