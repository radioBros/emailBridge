<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiLan } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import type { GatewayCreate, GatewayUpdate, GatewayView } from "@/api/gateways";
import { useGateways } from "@/features/gateways/composables/useGateways";
import { isAPIKind, kindLabelKey, tlsLabelKey } from "@/features/gateways/helpers/labels";
import GatewayFormDialog from "@/features/gateways/components/GatewayFormDialog.vue";
import GatewayRowActions from "@/features/gateways/components/GatewayRowActions.vue";
import GatewayVerifyPanel from "@/features/gateways/components/GatewayVerifyPanel.vue";
import DeliveryTestPanel from "@/features/gateways/components/DeliveryTestPanel.vue";

const { t, te } = useI18n();
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
  verifyResult,
  verifyError,
  verifying,
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
  runVerify,
  clearVerify,
} = useGateways();

const formOpen = ref(false);
const editing = ref<GatewayView | null>(null);
const verifyOpen = ref(false);
const verifyTarget = ref<GatewayView | null>(null);

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("gateways.col_name"), forced: true },
  { key: "kind", title: t("gateways.col_kind"), sortable: false },
  { key: "host", title: t("gateways.col_host"), sortable: false },
  { key: "enabled", title: t("gateways.col_enabled"), sortable: false },
  { key: "actions", title: t("gateways.col_actions"), sortable: false, align: "end" },
]);

const tlsLabel = (tls: string): string => {
  const key = tlsLabelKey(tls);
  return te(key) ? t(key) : tls;
};

const kindLabel = (kind: string): string => {
  const key = kindLabelKey(kind || "smtp");
  return te(key) ? t(key) : kind;
};

const hostLabel = (item: GatewayView): string => {
  if (isAPIKind(item.kind || "smtp")) {
    return item.api?.domain || item.api?.region || item.api?.from || item.kind;
  }
  return `${item.host ?? ""}:${item.port ?? ""}`;
};

const openCreate = (): void => {
  editing.value = null;
  formError.value = null;
  formOpen.value = true;
};

const openEdit = (row: GatewayView): void => {
  editing.value = row;
  formError.value = null;
  formOpen.value = true;
};

const onSubmit = async (
  payload: GatewayCreate | (GatewayUpdate & { id: string }),
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

const onDelete = async (row: GatewayView): Promise<void> => {
  const ok = await confirm({
    title: t("gateways.delete_title"),
    body: t("gateways.delete_body", { name: row.name }),
    variant: "danger",
    confirmText: t("gateways.delete"),
  });
  if (!ok) return;
  await remove(row.id);
};

const openVerify = (row: GatewayView): void => {
  verifyTarget.value = row;
  clearVerify();
  verifyOpen.value = true;
};

const onVerifyRun = async (): Promise<void> => {
  if (!verifyTarget.value) return;
  await runVerify(verifyTarget.value.id);
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
              :icon="mdiLan"
            />
          </v-avatar>
          {{ t("gateways.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("gateways.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        data-testid="gateways-create"
        @click="openCreate"
      >
        {{ t("gateways.create") }}
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
      :items="rows"
      :total="total"
      :page="page"
      :ipp="ipp"
      :sort-by="sortBy"
      :search="search"
      :loading="isFirstLoad || isRefreshing"
      :grid="{ xs: 1, md: 2 }"
      class="mb-6"
      table-name="smtp-gateways"
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
          {{ t("gateways.create") }}
        </v-btn>
      </template>

      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("gateways.empty") }}
        </div>
      </template>

      <template #card="{ item }">
        <v-card class="d-flex flex-column h-100">
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold text-wrap">
              {{ item.name }}
            </v-card-title>
            <v-card-subtitle class="text-wrap">
              {{ hostLabel(item) }}
            </v-card-subtitle>
          </v-card-item>
          <v-card-text class="flex-grow-1 pt-0">
            <div class="d-flex flex-wrap ga-2">
              <v-chip
                color="info"
              >
                {{ kindLabel(item.kind || "smtp") }}
              </v-chip>
              <v-chip
                v-if="!isAPIKind(item.kind || 'smtp')"
                color="secondary"
              >
                {{ tlsLabel(item.tls || "") }}
              </v-chip>
              <v-chip
                :color="item.enabled ? 'success' : 'warning'"
              >
                {{ item.enabled ? t("gateways.enabled_yes") : t("gateways.enabled_no") }}
              </v-chip>
            </div>
          </v-card-text>
          <v-card-actions class="px-4 pb-4 pt-0">
            <GatewayRowActions
              :item="item"
              @edit="openEdit(item)"
              @verify="openVerify(item)"
              @toggle="setEnabled(item, !item.enabled)"
              @delete="onDelete(item)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.name`]="{ item }">
        <span class="font-weight-medium">{{ item.name }}</span>
      </template>

      <template #[`item.kind`]="{ item }">
        <v-chip
          color="info"
        >
          {{ kindLabel(item.kind || "smtp") }}
        </v-chip>
      </template>

      <template #[`item.host`]="{ item }">
        <span class="font-mono text-body-small">{{ hostLabel(item) }}</span>
      </template>

      <template #[`item.enabled`]="{ item }">
        <v-chip
          :color="item.enabled ? 'success' : 'warning'"
        >
          {{ item.enabled ? t("gateways.enabled_yes") : t("gateways.enabled_no") }}
        </v-chip>
      </template>

      <template #[`item.actions`]="{ item }">
        <GatewayRowActions
          :item="item"
          @edit="openEdit(item)"
          @verify="openVerify(item)"
          @toggle="setEnabled(item, !item.enabled)"
          @delete="onDelete(item)"
        />
      </template>
    </TableCard>

    <DeliveryTestPanel />

    <GatewayFormDialog
      v-model="formOpen"
      :gateway="editing"
      :submitting="mutating"
      :error-msg="formError"
      :field-errors="formFieldErrors"
      @submit="onSubmit"
    />

    <GatewayVerifyPanel
      v-model="verifyOpen"
      :gateway="verifyTarget"
      :result="verifyResult"
      :error-msg="verifyError"
      :loading="verifying"
      @run="onVerifyRun"
    />
  </div>
</template>
