<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiApacheKafka,
  mdiAws,
  mdiGoogleCloud,
  mdiServerNetwork,
  mdiTransitConnectionVariant,
  mdiWebhook,
} from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import {
  BreakerStates,
  DeliveryStates,
  type BreakerState,
  type DeliveryState,
} from "@/constants/Statuses";
import { EMPTY_VALUE } from "@/helpers/Format";
import DateUtil from "@/helpers/Date";
import type { EndpointCreate, EndpointUpdate, EndpointView } from "@/api/webhooks";
import { useWebhooks } from "@/features/webhooks/composables/useWebhooks";
import WebhookFormDialog from "@/features/webhooks/components/WebhookFormDialog.vue";
import SecretRevealDialog from "@/features/webhooks/components/SecretRevealDialog.vue";
import WebhookTestPanel from "@/features/webhooks/components/WebhookTestPanel.vue";
import WebhookRowActions from "@/features/webhooks/components/WebhookRowActions.vue";

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
  revealedSecret,
  testResult,
  testError,
  isFirstLoad,
  isRefreshing,
  load,
  setPage,
  setIpp,
  setSortBy,
  setSearch,
  create,
  update,
  remove,
  setEnabled,
  rotateSecret,
  runTest,
  clearSecret,
  clearTest,
} = useWebhooks();

const formOpen = ref(false);
const editing = ref<EndpointView | null>(null);
const secretOpen = ref(false);
const testOpen = ref(false);
const testTarget = ref<EndpointView | null>(null);

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("webhooks.col_name"), forced: true },
  { key: "url", title: t("webhooks.col_url"), sortable: false },
  { key: "events", title: t("webhooks.col_events"), sortable: false },
  { key: "breaker", title: t("webhooks.col_breaker"), sortable: false },
  { key: "lastDelivery", title: t("webhooks.col_last"), sortable: false },
  { key: "actions", title: t("webhooks.col_actions"), sortable: false, align: "end" },
]);

const tableItems = computed(() => rows.value);

const breakerOf = (state: string) =>
  BreakerStates[(state as BreakerState) || "closed"] ?? BreakerStates.closed;

const breakerLabel = (state: string): string => {
  if (state === "open") return t("webhooks.breaker_open");
  if (state === "halfOpen") return t("webhooks.breaker_halfOpen");
  return t("webhooks.breaker_closed");
};

const kindLabel = (kind: string): string => {
  const key = `webhooks.kind_${kind}`;
  return t(key) !== key ? t(key) : kind;
};

const kindIcon = (kind: string): string => {
  switch (kind) {
    case "nats":
      return mdiTransitConnectionVariant;
    case "kafka":
      return mdiApacheKafka;
    case "sqs":
    case "sns":
      return mdiAws;
    case "pubsub":
      return mdiGoogleCloud;
    case "http":
      return mdiWebhook;
    default:
      return mdiServerNetwork;
  }
};

const deliveryOf = (state: string) =>
  DeliveryStates[(state as DeliveryState)] ?? DeliveryStates.pending;

const deliveryLabel = (state: string): string => {
  const key = `webhooks.delivery_${state}`;
  return t(key) !== key ? t(key) : state;
};

const relativeAt = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const disabledAt = (iso: string): string => DateUtil.formatDateTime(iso);

const openCreate = (): void => {
  editing.value = null;
  formError.value = null;
  formOpen.value = true;
};

const openEdit = (row: EndpointView): void => {
  editing.value = row;
  formError.value = null;
  formOpen.value = true;
};

const onSubmit = async (
  payload: EndpointCreate | (EndpointUpdate & { id: string }),
): Promise<void> => {
  if ("id" in payload) {
    const { id, ...body } = payload;
    const ok = await update(id, body);
    if (ok) formOpen.value = false;
    return;
  }
  const created = await create(payload);
  if (created) {
    formOpen.value = false;
    if (revealedSecret.value) secretOpen.value = true;
  }
};

const onDelete = async (row: EndpointView): Promise<void> => {
  const ok = await confirm({
    title: t("webhooks.delete_title"),
    body: t("webhooks.delete_body", { name: row.name }),
    variant: "danger",
    confirmText: t("webhooks.delete"),
  });
  if (!ok) return;
  await remove(row.id);
};

const onRotate = async (row: EndpointView): Promise<void> => {
  const ok = await confirm({
    title: t("webhooks.rotate_title"),
    body: t("webhooks.rotate_body"),
    variant: "warning",
    confirmText: t("webhooks.rotate"),
  });
  if (!ok) return;
  const result = await rotateSecret(row.id);
  if (result) secretOpen.value = true;
};

const onToggle = async (row: EndpointView): Promise<void> => {
  await setEnabled(row, !row.enabled);
};

const openTest = (row: EndpointView): void => {
  testTarget.value = row;
  clearTest();
  testOpen.value = true;
};

const onSecretClose = (v: boolean): void => {
  secretOpen.value = v;
  if (!v) clearSecret();
};
</script>

<template>
  <div>
    <div class="d-flex align-start justify-space-between flex-wrap ga-3 mb-6">
      <div class="min-w-0">
        <h1 class="text-h5 font-weight-bold mb-1 d-flex align-center ga-2">
          <v-avatar
            color="info"
          >
            <v-icon
              :icon="mdiWebhook"
            />
          </v-avatar>
          {{ t("webhooks.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("webhooks.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        data-testid="webhooks-create"
        @click="openCreate"
      >
        {{ t("webhooks.create") }}
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
      table-name="webhook-endpoints"
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
          {{ t("webhooks.create") }}
        </v-btn>
      </template>

      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("webhooks.empty") }}
        </div>
      </template>

      <template #card="{ item }">
        <v-card class="d-flex flex-column h-100">
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold text-wrap">
              {{ item.name }}
            </v-card-title>
            <v-card-subtitle class="text-wrap">
              {{ item.url || EMPTY_VALUE }}
            </v-card-subtitle>
          </v-card-item>
          <v-card-text class="flex-grow-1 pt-0">
            <div class="d-flex flex-wrap align-center ga-2 mb-2">
              <v-chip
                :prepend-icon="kindIcon(item.kind)"
                color="secondary"
              >
                {{ kindLabel(item.kind) }}
              </v-chip>
              <v-chip
                :color="breakerOf(item.breaker.state).color"
                :prepend-icon="breakerOf(item.breaker.state).icon"
              >
                {{ breakerLabel(item.breaker.state) }}
              </v-chip>
              <v-chip
                v-if="item.disabledReason"
                v-tippy="disabledAt(item.disabledReason.at)"
                color="error"
              >
                {{ t("webhooks.disabled_chip", { reason: item.disabledReason.code }) }}
              </v-chip>
            </div>
            <div
              v-if="item.lastDelivery"
              class="d-flex flex-wrap align-center ga-2"
            >
              <v-chip
                :color="deliveryOf(item.lastDelivery.state).color"
                :prepend-icon="deliveryOf(item.lastDelivery.state).icon"
              >
                {{ deliveryLabel(item.lastDelivery.state) }}
                <span
                  v-if="item.lastDelivery.statusCode"
                  class="ms-1"
                >{{ item.lastDelivery.statusCode }}</span>
              </v-chip>
              <span class="text-caption text-medium-emphasis">
                {{ relativeAt(item.lastDelivery.at) }}
              </span>
            </div>
            <span
              v-else
              class="text-caption text-medium-emphasis"
            >{{ t("webhooks.last_unknown") }}</span>
          </v-card-text>
          <v-card-actions class="px-4 pb-4 pt-0">
            <WebhookRowActions
              :item="item"
              @edit="openEdit(item)"
              @test="openTest(item)"
              @rotate="onRotate(item)"
              @toggle="onToggle(item)"
              @delete="onDelete(item)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.name`]="{ item }">
        <div class="d-flex flex-wrap align-center ga-2 min-w-0">
          <span class="text-wrap font-weight-medium">
            {{ item.name }}
          </span>
          <v-chip
            v-if="item.disabledReason"
            v-tippy="disabledAt(item.disabledReason.at)"
            color="error"
          >
            {{ t("webhooks.disabled_chip", { reason: item.disabledReason.code }) }}
          </v-chip>
        </div>
      </template>

      <template #[`item.url`]="{ item }">
        <div class="min-w-0">
          <div class="text-truncate">
            {{ item.url || EMPTY_VALUE }}
          </div>
          <v-chip
            :prepend-icon="kindIcon(item.kind)"
            color="secondary"
            class="mt-1"
          >
            {{ kindLabel(item.kind) }}
          </v-chip>
        </div>
      </template>

      <template #[`item.events`]="{ item }">
        <div class="d-flex flex-wrap ga-1">
          <v-chip
            v-for="ev in (item.events ?? []).slice(0, 3)"
            :key="ev"
            color="primary"
          >
            {{ ev }}
          </v-chip>
          <v-chip
            v-if="(item.events?.length ?? 0) > 3"
          >
            {{ t("webhooks.events_more", { n: (item.events?.length ?? 0) - 3 }) }}
          </v-chip>
        </div>
      </template>

      <template #[`item.breaker`]="{ item }">
        <v-chip
          :color="breakerOf(item.breaker.state).color"
          :prepend-icon="breakerOf(item.breaker.state).icon"
        >
          {{ breakerLabel(item.breaker.state) }}
        </v-chip>
      </template>

      <template #[`item.lastDelivery`]="{ item }">
        <div
          v-if="item.lastDelivery"
          class="d-flex flex-column ga-1"
        >
          <v-chip
            :color="deliveryOf(item.lastDelivery.state).color"
            :prepend-icon="deliveryOf(item.lastDelivery.state).icon"
          >
            {{ deliveryLabel(item.lastDelivery.state) }}
            <span
              v-if="item.lastDelivery.statusCode"
              class="ms-1"
            >{{ item.lastDelivery.statusCode }}</span>
          </v-chip>
          <span class="text-caption text-medium-emphasis">
            {{ relativeAt(item.lastDelivery.at) }}
          </span>
        </div>
        <span
          v-else
          class="text-medium-emphasis"
        >{{ t("webhooks.last_unknown") }}</span>
      </template>

      <template #[`item.actions`]="{ item }">
        <WebhookRowActions
          :item="item"
          @edit="openEdit(item)"
          @test="openTest(item)"
          @rotate="onRotate(item)"
          @toggle="onToggle(item)"
          @delete="onDelete(item)"
        />
      </template>
    </TableCard>

    <WebhookFormDialog
      v-model="formOpen"
      :endpoint="editing"
      :submitting="mutating"
      :error-msg="formError"
      :field-errors="formFieldErrors"
      @submit="onSubmit"
    />

    <SecretRevealDialog
      :model-value="secretOpen"
      :secret="revealedSecret"
      @update:model-value="onSecretClose"
    />

    <AppDialog
      v-model="testOpen"
      :max-width="560"
    >
      <template #title>
        {{ t("webhooks.test_title") }}
        <span
          v-if="testTarget"
          class="text-medium-emphasis text-body-2 d-block"
        >
          {{ testTarget.name }}
        </span>
      </template>

      <WebhookTestPanel
        v-if="testTarget"
        :result="testResult"
        :error="testError"
        :loading="mutating"
        @run="runTest(testTarget.id)"
      />

      <template #footer>
        <v-btn @click="testOpen = false">
          {{ t("webhooks.cancel") }}
        </v-btn>
      </template>
    </AppDialog>
  </div>
</template>
