<script setup lang="ts">
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiMailboxOutline, mdiPlus } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import AppCard from "@/components/cards/AppCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import AccountStateChip from "@/features/accounts/components/AccountStateChip.vue";
import AccountRowActions from "@/features/accounts/components/AccountRowActions.vue";
import {
  useAccountsList,
  type AccountRow,
} from "@/features/accounts/composables/useAccountsList";

const { t } = useI18n();
const router = useRouter();
const confirm = useConfirm();
const createAction = actionOf("create");

const {
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  filterState,
  stateOptions,
  loadError,
  empty,
  isLoading,
  mutatingId,
  load,
  doSync,
  doReconnect,
  doResume,
  doDelete,
} = useAccountsList();

onMounted(() => {
  void load();
});

const headers = computed<ServerTableHeader[]>(() => [
  { key: "name", title: t("accounts.col_name"), sortable: true },
  { key: "email", title: t("accounts.col_email"), sortable: true },
  { key: "externalId", title: t("accounts.col_external_id"), sortable: false },
  { key: "state", title: t("accounts.col_state"), sortable: true },
  { key: "ownerNode", title: t("accounts.col_owner"), sortable: false },
  { key: "lastSyncAt", title: t("accounts.col_last_sync"), sortable: true },
  { key: "actions", title: t("accounts.col_actions"), sortable: false, forced: true },
]);

const stateItems = computed(() => [
  { title: t("accounts.filter_all_states"), value: "" },
  ...stateOptions.map((s) => ({ title: t(`accounts.state_${s}`), value: s })),
]);

const formatSync = (iso?: string): string =>
  iso ? DateUtil.formatDateTime(iso) : EMPTY_VALUE;

const onDelete = async (row: AccountRow): Promise<void> => {
  const ok = await confirm({
    title: t("accounts.delete_title"),
    body: t("accounts.delete_body"),
    variant: "danger",
    confirmText: t("accounts.action_delete"),
  });
  if (!ok) return;
  await doDelete(row);
};

const openDetail = (_e: Event, row: { item: AccountRow }): void => {
  void router.push(`/accounts/${row.item.id}`);
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-start justify-space-between ga-4 mb-6">
      <div class="min-w-0">
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("accounts.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("accounts.subtitle") }}
        </p>
      </div>
      <v-btn
        :color="createAction.color"
        :prepend-icon="mdiPlus"
        to="/accounts/new"
      >
        {{ t("accounts.create") }}
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
          :color="actionOf('retry').color"
          @click="load"
        >
          {{ t("accounts.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <AppCard
      v-else-if="empty && !filterState && !search"
      class="mb-4"
    >
      <div class="text-center py-8 px-4">
        <v-avatar-hero
          color="success"
          class="mb-4"
        >
          <v-icon
            :icon="mdiMailboxOutline"
          />
        </v-avatar-hero>
        <p class="text-body-large mb-4">
          {{ t("accounts.empty") }}
        </p>
        <v-btn
          :color="createAction.color"
          :prepend-icon="mdiPlus"
          to="/accounts/new"
        >
          {{ t("accounts.create") }}
        </v-btn>
      </div>
    </AppCard>

    <TableCard
      v-else
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      v-model:search="search"
      :headers="headers"
      :items="items"
      :total="total"
      :loading="isLoading"
      table-name="accounts"
      item-value="id"
      @click:row="openDetail"
    >
      <template #toolbar>
        <AutocompleteSearch
          v-model="filterState"
          :items="stateItems"
          :label="t('accounts.filter_state')"
          class="no-validation flex-grow-1 w-100"
          clearable
          hide-details
        />
      </template>

      <template #card="{ item, clickable, select }">
        <v-card
          :link="clickable"
          class="d-flex flex-column h-100"
          @click="select"
        >
          <v-card-item class="pb-2">
            <v-card-title class="text-title-medium font-weight-bold">
              {{ item.name }}
            </v-card-title>
            <v-card-subtitle>
              {{ item.email }}
            </v-card-subtitle>
          </v-card-item>
          <v-card-text class="flex-grow-1 pt-0">
            <div class="d-flex flex-wrap align-center ga-2 mb-2">
              <AccountStateChip :state="String(item.state)" />
              <span class="text-body-small text-medium-emphasis">
                {{ formatSync(item.lastSyncAt as string | undefined) }}
              </span>
            </div>
            <div class="text-body-small text-medium-emphasis">
              {{ t("accounts.col_owner") }}:
              <span v-if="item.ownerNode">{{ item.ownerNode }}</span>
              <span
                v-else
                v-tippy="t('accounts.owner_none_tip')"
              >{{ EMPTY_VALUE }}</span>
            </div>
          </v-card-text>
          <v-card-actions class="px-4 pb-4 pt-0">
            <AccountRowActions
              :state="String(item.state)"
              :account-id="String(item.id)"
              :loading="mutatingId === item.id"
              @sync="(mode) => doSync(String(item.id), mode)"
              @reconnect="doReconnect(String(item.id))"
              @resume="doResume(String(item.id))"
              @delete="onDelete(item as AccountRow)"
            />
          </v-card-actions>
        </v-card>
      </template>

      <template #[`item.state`]="{ item }">
        <AccountStateChip :state="item.state" />
      </template>

      <template #[`item.externalId`]="{ item }">
        {{ item.externalId || EMPTY_VALUE }}
      </template>

      <template #[`item.ownerNode`]="{ item }">
        <span v-if="item.ownerNode">{{ item.ownerNode }}</span>
        <span
          v-else
          v-tippy="t('accounts.owner_none_tip')"
        >{{ EMPTY_VALUE }}</span>
      </template>

      <template #[`item.lastSyncAt`]="{ item }">
        {{ formatSync(item.lastSyncAt) }}
      </template>

      <template #[`item.actions`]="{ item }">
        <AccountRowActions
          :state="item.state"
          :account-id="item.id"
          :loading="mutatingId === item.id"
          @sync="(mode) => doSync(item.id, mode)"
          @reconnect="doReconnect(item.id)"
          @resume="doResume(item.id)"
          @delete="onDelete(item)"
        />
      </template>

      <template #no-data>
        <div class="text-center py-6 text-medium-emphasis">
          {{ t("accounts.empty_filtered") }}
        </div>
      </template>
    </TableCard>
  </div>
</template>
