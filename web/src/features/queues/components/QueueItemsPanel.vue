<script setup lang="ts">
import { computed, onMounted, ref, toRef } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiAlertCircle, mdiFilterVariant, mdiRefresh, mdiTrayRemove } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import DateRangeField from "@/components/DateRangeField.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import { useSessionStore } from "@/stores/session";
import { useNotifyStore } from "@/stores/notify";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useConfirm } from "@/composables/useConfirm";
import { EMPTY_VALUE } from "@/helpers/Format";
import { listWebhookEndpoints } from "@/api/webhooks";
import { searchAccounts, type AccountView } from "@/api/outbox";
import type { QueueScope } from "@/api/queues";
import { accountLabelOf, endpointLabelOf } from "@/features/deliveries/helpers/labelCache";
import CancelQueueDialog from "@/features/queues/components/CancelQueueDialog.vue";
import QueueStateChip from "@/features/queues/components/QueueStateChip.vue";
import { useQueueItems } from "@/features/queues/composables/useQueueItems";
import { queueDefOf, QUEUE_DEFS } from "@/features/queues/helpers/queues";

const props = defineProps<{
  queue: string;
  scope: QueueScope;
  canCancel: boolean;
  isOwner: boolean;
}>();

const emit = defineEmits<{ changed: [] }>();

const { t, te } = useI18n();
const { mdAndUp } = useDisplay();
const cancelAction = actionOf("cancel");
const destroyAction = actionOf("delete");
const retryAction = actionOf("retry");

const def = queueDefOf(props.queue) ?? QUEUE_DEFS[0]!;
const list = useQueueItems(def, toRef(props, "scope"), () => emit("changed"));
const {
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  filterState,
  filterAccountId,
  filterEndpointId,
  filterFrom,
  filterTo,
  hasFilters,
  activeFilterCount,
  selected,
  allSelectedOnPage,
  cancelling,
  cancellingId,
  job,
  loadError,
  empty,
  noResults,
  isFirstLoad,
  isRefreshing,
  load,
  cancelOne,
  cancelSelected,
  cancelMatching,
  cancelAll,
  toggleSelect,
  clearFilters,
  onFilterChange,
} = list;

defineExpose({ load });

const confirm = useConfirm();

// Cancelling items is irreversible: single and selected items ask first, like the whole-queue dialog.
const confirmCancelOne = async (id: string): Promise<void> => {
  const ok = await confirm({
    title: t("queues.cancel_one_title"),
    body: t("queues.cancel_one_body"),
    variant: "warning",
    confirmText: t("queues.cancel_row"),
  });
  if (ok) await cancelOne(id);
};

const confirmCancelSelected = async (): Promise<void> => {
  const ok = await confirm({
    title: t("queues.cancel_selected_title", { n: selected.value.length }),
    body: t("queues.cancel_selected_body"),
    variant: "warning",
    confirmText: t("queues.cancel_selected"),
  });
  if (ok) await cancelSelected();
};

const filtersOpen = ref(false);
const matchingOpen = ref(false);
const entireOpen = ref(false);
const accountOptions = ref<AccountView[]>([]);
const endpointOptions = ref<{ id: string; name: string }[]>([]);
const accountsLoading = ref(false);
const endpointsLoading = ref(false);

const session = useSessionStore();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const optionsFailed = (err: unknown): void => {
  notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
};
const queueLabel = computed(() => t(`queues.queue_${def.key}`));
/** Organisation names the caller knows (its memberships); other organisations fall back to the ID. */
const orgNames = computed(
  () => new Map((session.me?.memberships ?? []).map((m) => [m.organizationId, m.organizationName])),
);
const allOrgs = computed(() => props.scope === "all");

const headers = computed<ServerTableHeader[]>(() => {
  const h: ServerTableHeader[] = [];
  if (props.canCancel) h.push({ key: "select", title: "", sortable: false, width: 56, minWidth: 56 });
  h.push({ key: "createdAt", title: t("queues.col_created"), sortable: true });
  h.push({ key: "state", title: t("queues.col_state"), sortable: false, forced: true });
  if (def.key === "webhooks") {
    h.push({ key: "eventType", title: t("queues.col_event"), sortable: false });
    h.push({ key: "endpointId", title: t("queues.col_endpoint"), sortable: false });
  } else {
    h.push({ key: "recipient", title: t("queues.col_recipient"), sortable: false });
  }
  if (def.key === "systemMail") h.push({ key: "mailKind", title: t("queues.col_kind"), sortable: false });
  if (def.hasAccount) h.push({ key: "accountId", title: t("queues.col_account"), sortable: false });
  h.push({ key: "nextAttemptAt", title: t("queues.col_next"), sortable: true });
  h.push({ key: "actions", title: t("queues.col_actions"), sortable: false, align: "end" });
  return h;
});

const stateItems = computed(() =>
  def.states.map((s) => ({ title: te(`queues.state_${s}`) ? t(`queues.state_${s}`) : s, value: s })),
);

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({ title: a.email ? `${a.name} · ${a.email}` : a.name, value: a.id })),
);
const endpointItems = computed(() => endpointOptions.value.map((e) => ({ title: e.name, value: e.id })));

const tableItems = computed(() =>
  items.value.map((row) => ({
    ...row,
    accountName: row.accountId ? accountLabelOf(row.accountId) : EMPTY_VALUE,
    endpointName: row.endpointId ? endpointLabelOf(row.endpointId) : EMPTY_VALUE,
    orgName: row.organizationId ? (orgNames.value.get(row.organizationId) ?? row.organizationId) : EMPTY_VALUE,
  })),
);

const loadAccounts = async (q = ""): Promise<void> => {
  accountsLoading.value = true;
  try {
    accountOptions.value = await searchAccounts(q);
  } catch (err) {
    accountOptions.value = [];
    optionsFailed(err);
  } finally {
    accountsLoading.value = false;
  }
};

const loadEndpoints = async (q = ""): Promise<void> => {
  endpointsLoading.value = true;
  try {
    const res = await listWebhookEndpoints({ page: 1, limit: 50, q: q || undefined });
    endpointOptions.value = (res.data ?? []).map((e) => ({ id: e.id, name: e.name }));
  } catch (err) {
    endpointOptions.value = [];
    optionsFailed(err);
  } finally {
    endpointsLoading.value = false;
  }
};

const nextOf = (row: { state: string; sendAt?: string; nextAttemptAt?: string }): string => {
  const at = row.state === "scheduled" ? row.sendAt : row.nextAttemptAt;
  return at ? formatDateTime(at) : EMPTY_VALUE;
};

const mailKindLabel = (kind: string | undefined): string => {
  if (!kind) return EMPTY_VALUE;
  const key = `queues.mail_${kind}`;
  return te(key) ? t(key) : kind;
};

const jobPercent = computed(() => {
  const j = job.value;
  if (!j || !j.total) return 0;
  return Math.min(100, Math.round((j.done / j.total) * 100));
});

const onConfirmMatching = async (): Promise<void> => {
  if (await cancelMatching()) matchingOpen.value = false;
};

const onConfirmEntire = async (typed: string): Promise<void> => {
  if (await cancelAll(typed)) entireOpen.value = false;
};

onMounted(() => {
  void load();
  if (def.hasAccount) void loadAccounts();
  if (def.hasEndpoint) void loadEndpoints();
});
</script>

<template>
  <div class="d-flex flex-column ga-4">
    <!-- Filters: inline from md, a bottom sheet on phones -->
    <template v-if="!mdAndUp">
      <v-btn
        :prepend-icon="mdiFilterVariant"
        color="primary"
        block
        @click="filtersOpen = true"
      >
        {{ t("queues.filters") }}
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
          :title="t('queues.filters')"
          as="sheet"
        >
          <div class="d-flex flex-column ga-3">
            <v-select
              v-model="filterState"
              :items="stateItems"
              :label="t('queues.filter_state')"
              class="no-validation"
              clearable
              hide-details
              @update:model-value="onFilterChange"
            />
            <AutocompleteSearch
              v-if="def.hasAccount"
              v-model="filterAccountId"
              :items="accountItems"
              :loading="accountsLoading"
              :label="t('queues.filter_account')"
              class="no-validation"
              server-search
              clearable
              hide-details
              @update:search="loadAccounts"
              @update:model-value="onFilterChange"
            />
            <AutocompleteSearch
              v-if="def.hasEndpoint"
              v-model="filterEndpointId"
              :items="endpointItems"
              :loading="endpointsLoading"
              :label="t('queues.filter_endpoint')"
              class="no-validation"
              server-search
              clearable
              hide-details
              @update:search="loadEndpoints"
              @update:model-value="onFilterChange"
            />
            <DateRangeField
              v-model:from="filterFrom"
              v-model:to="filterTo"
              :label="t('queues.filter_dates')"
              @change="onFilterChange"
            />
          </div>
          <template #footer>
            <v-btn
              v-if="activeFilterCount"
              @click="clearFilters"
            >
              {{ t("queues.clear_filters") }}
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
    </template>

    <v-row v-else>
      <v-col cols="12" md="6" lg="3">
        <v-select
          v-model="filterState"
          :items="stateItems"
          :label="t('queues.filter_state')"
          class="no-validation"
          clearable
          hide-details
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        v-if="def.hasAccount"
        cols="12"
        md="6"
        lg="3"
      >
        <AutocompleteSearch
          v-model="filterAccountId"
          :items="accountItems"
          :loading="accountsLoading"
          :label="t('queues.filter_account')"
          class="no-validation"
          server-search
          clearable
          hide-details
          @update:search="loadAccounts"
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        v-if="def.hasEndpoint"
        cols="12"
        md="6"
        lg="3"
      >
        <AutocompleteSearch
          v-model="filterEndpointId"
          :items="endpointItems"
          :loading="endpointsLoading"
          :label="t('queues.filter_endpoint')"
          class="no-validation"
          server-search
          clearable
          hide-details
          @update:search="loadEndpoints"
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col cols="12" md="auto">
        <DateRangeField
          v-model:from="filterFrom"
          v-model:to="filterTo"
          :label="t('queues.filter_dates')"
          @change="onFilterChange"
        />
      </v-col>
    </v-row>

    <!-- Bulk actions -->
    <div
      v-if="canCancel"
      class="d-flex flex-wrap align-center ga-2"
      data-testid="queue-bulk-bar"
    >
      <span class="text-body-medium text-medium-emphasis me-auto">
        {{ selected.length ? t("queues.selected_n", { n: selected.length }) : t("queues.select_hint") }}
      </span>
      <v-btn
        :color="cancelAction.color"
        :prepend-icon="cancelAction.icon"
        :disabled="!selected.length || Boolean(job)"
        :loading="cancelling && Boolean(selected.length)"
        data-testid="queue-cancel-selected"
        @click="confirmCancelSelected"
      >
        {{ t("queues.cancel_selected") }}
      </v-btn>
      <v-btn
        :color="cancelAction.color"
        :prepend-icon="cancelAction.icon"
        :disabled="!hasFilters || Boolean(job)"
        data-testid="queue-cancel-matching"
        @click="matchingOpen = true"
      >
        {{ t("queues.cancel_matching") }}
      </v-btn>
      <v-btn
        v-if="isOwner"
        :color="destroyAction.color"
        :prepend-icon="mdiTrayRemove"
        :disabled="Boolean(job)"
        data-testid="queue-cancel-entire"
        @click="entireOpen = true"
      >
        {{ t("queues.cancel_entire") }}
      </v-btn>
    </div>

    <!-- Running bulk cancel -->
    <AppCard
      v-if="job"
      class="h-auto"
      data-testid="queue-job-progress"
    >
      <div class="d-flex flex-column ga-2">
        <div class="d-flex align-center justify-space-between ga-3 text-body-medium">
          <span>{{ t("queues.job_running") }}</span>
          <span class="text-numeric text-medium-emphasis">
            {{ t("queues.job_counts", { done: job.done, total: job.total }) }}
          </span>
        </div>
        <v-progress-linear
          :model-value="jobPercent"
          :indeterminate="!job.total"
          :aria-label="t('queues.job_running')"
          bg-color="surface-variant"
          color="primary"
          rounded
        />
      </div>
    </AppCard>

    <AppCard
      v-if="loadError"
      class="h-auto"
    >
      <div class="d-flex align-center ga-4 pa-2">
        <v-avatar-lead color="error">
          <v-icon :icon="mdiAlertCircle" />
        </v-avatar-lead>
        <div class="flex-grow-1 text-body-large">
          {{ loadError }}
        </div>
        <v-btn
          :color="retryAction.color"
          :prepend-icon="mdiRefresh"
          @click="load"
        >
          {{ t("queues.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <AppCard
      v-else-if="empty"
      class="h-auto"
      data-testid="queue-empty"
    >
      <div class="d-flex flex-column align-center text-center pa-6 ga-3">
        <v-avatar-hero color="success">
          <v-icon :icon="def.icon" />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("queues.empty_title", { queue: queueLabel }) }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t(`queues.empty_body_${def.key}`) }}
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
      :table-name="`queue-items-${def.key}`"
      item-value="id"
      searchable
    >
      <template #no-data>
        <div class="text-center pa-4">
          <div class="text-body-medium mb-2">
            {{ noResults ? t("queues.no_results") : t("tables.no_results") }}
          </div>
          <v-btn
            v-if="noResults"
            color="primary"
            @click="clearFilters"
          >
            {{ t("queues.clear_filters") }}
          </v-btn>
        </div>
      </template>
      <template #[`header.select`]>
        <v-checkbox
          v-model="allSelectedOnPage"
          :aria-label="t('queues.select_all')"
          class="no-validation ma-0 pa-0"
          hide-details
          @click.stop
        />
      </template>
      <template #[`item.select`]="{ item }">
        <v-checkbox
          :model-value="selected.includes(String(item.id))"
          :disabled="!item.cancellable"
          :aria-label="t('queues.select_row')"
          class="no-validation ma-0 pa-0"
          hide-details
          @click.stop
          @update:model-value="toggleSelect(String(item.id), Boolean($event))"
        />
      </template>
      <template #[`item.createdAt`]="{ item }">
        <span class="text-numeric text-no-wrap">{{ formatDateTime(item.createdAt) }}</span>
      </template>
      <template #[`item.state`]="{ item }">
        <QueueStateChip :state="String(item.state)" />
      </template>
      <template #[`item.recipient`]="{ item }">
        <div class="min-w-0">
          <div class="text-truncate">
            {{ item.recipient || EMPTY_VALUE }}
            <span
              v-if="Number(item.recipientCount) > 1"
              class="text-medium-emphasis"
            >
              {{ t("queues.more_recipients", { n: Number(item.recipientCount) - 1 }) }}
            </span>
          </div>
          <div
            v-if="item.subject"
            class="text-body-small text-medium-emphasis text-truncate"
          >
            {{ item.subject }}
          </div>
        </div>
      </template>
      <template #[`item.eventType`]="{ item }">
        <span class="text-numeric">{{ item.eventType || EMPTY_VALUE }}</span>
      </template>
      <template #[`item.endpointId`]="{ item }">
        {{ item.endpointName }}
      </template>
      <template #[`item.accountId`]="{ item }">
        <div class="min-w-0">
          <div class="text-truncate">
            {{ item.accountName }}
          </div>
          <div
            v-if="allOrgs"
            class="text-body-small text-medium-emphasis text-truncate"
          >
            {{ item.orgName }}
          </div>
        </div>
      </template>
      <template #[`item.mailKind`]="{ item }">
        {{ mailKindLabel(item.mailKind as string | undefined) }}
      </template>
      <template #[`item.nextAttemptAt`]="{ item }">
        <div class="text-no-wrap">
          <div class="text-numeric">
            {{ nextOf(item as never) }}
          </div>
          <div class="text-body-small text-medium-emphasis">
            {{ t("queues.attempts_n", { n: Number(item.attempts) }) }}
          </div>
        </div>
      </template>
      <template #[`item.actions`]="{ item }">
        <span
          v-if="!item.cancellable"
          class="text-body-small text-medium-emphasis text-no-wrap"
        >
          {{ t("queues.in_flight") }}
        </span>
        <v-btn
          v-else-if="canCancel"
          :color="cancelAction.color"
          :icon="cancelAction.icon"
          :aria-label="t('queues.cancel_row')"
          :loading="cancellingId === item.id"
          :disabled="Boolean(job)"
          data-testid="queue-cancel-row"
          @click.stop="confirmCancelOne(String(item.id))"
        />
      </template>
    </TableCard>

    <CancelQueueDialog
      v-model="matchingOpen"
      :title="t('queues.cancel_matching_title', { queue: queueLabel })"
      :confirm-label="t('queues.cancel_matching')"
      :loading="cancelling"
      @confirm="onConfirmMatching"
    >
      <p class="text-body-medium mb-0">
        {{ t("queues.cancel_matching_body", { n: total }) }}
      </p>
    </CancelQueueDialog>

    <CancelQueueDialog
      v-model="entireOpen"
      :title="t('queues.cancel_entire_title', { queue: queueLabel })"
      :confirm-label="t('queues.cancel_entire')"
      :type-to-confirm="def.name"
      :loading="cancelling"
      @confirm="onConfirmEntire"
    >
      <p class="text-body-medium mb-0">
        {{ allOrgs ? t("queues.cancel_entire_body_all") : t("queues.cancel_entire_body_org") }}
      </p>
    </CancelQueueDialog>
  </div>
</template>
