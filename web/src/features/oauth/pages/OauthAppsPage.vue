<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCheckDecagram, mdiPencil, mdiPlus, mdiTrashCan } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { useConfirm } from "@/composables/useConfirm";
import { actionOf } from "@/constants/Actions";
import { EMPTY_VALUE } from "@/helpers/Format";
import DateUtil from "@/helpers/Date";
import type { OauthAppCreate, OauthAppPatch } from "@/api/oauth";
import { useOauthApps, type OauthAppRow } from "@/features/oauth/composables/useOauthApps";
import OauthAppFormDialog from "@/features/oauth/components/OauthAppFormDialog.vue";

const { t } = useI18n();
const confirm = useConfirm();
const createAction = actionOf("create");

const {
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  loadError,
  forbidden,
  mutating,
  tableLoading,
  load,
  onSearch,
  setPage,
  setIpp,
  setSortBy,
  create,
  update,
  remove,
  verify,
} = useOauthApps();

const formOpen = ref(false);
const editing = ref<OauthAppRow | null>(null);

onMounted(() => {
  void load();
});

const headers = computed((): ServerTableHeader[] => [
  { key: "name", title: t("oauth.col_name"), forced: true },
  { key: "provider", title: t("oauth.col_provider") },
  { key: "clientId", title: t("oauth.col_client"), sortable: false },
  { key: "enabled", title: t("oauth.col_enabled"), sortable: false },
  // Default sort key: a header of its own so the phone sort select shows a label, not "createdAt".
  { key: "createdAt", title: t("oauth.col_created"), sortable: true },
  { key: "actions", title: t("oauth.col_actions"), sortable: false, align: "end" },
]);

const providerLabel = (p: string): string => {
  const key = `oauth.provider_${p}`;
  return t(key) !== key ? t(key) : p;
};

const formatWhen = (iso: string): string => {
  if (!iso) return EMPTY_VALUE;
  return DateUtil.format(iso, "dd/MM/yyyy HH:mm") || EMPTY_VALUE;
};

const openCreate = (): void => {
  editing.value = null;
  formOpen.value = true;
};

const openEdit = (row: OauthAppRow): void => {
  editing.value = row;
  formOpen.value = true;
};

const onSave = async (body: OauthAppCreate | OauthAppPatch): Promise<void> => {
  const ok = editing.value
    ? await update(editing.value, body as OauthAppPatch)
    : await create(body as OauthAppCreate);
  if (ok) {
    formOpen.value = false;
    editing.value = null;
  }
};

const onDelete = async (row: OauthAppRow): Promise<void> => {
  const ok = await confirm({
    title: t("oauth.delete_title"),
    body: t("oauth.delete_body", { name: row.name }),
    variant: "danger",
    confirmText: t("oauth.delete"),
  });
  if (!ok) return;
  await remove(row);
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-4 mb-6">
      <div class="min-width-0">
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("oauth.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("oauth.subtitle") }}
        </p>
      </div>
      <v-btn
        v-if="!forbidden"
        :color="createAction.color"
        :prepend-icon="mdiPlus"
        class="flex-shrink-0"
        @click="openCreate"
      >
        {{ t("oauth.create") }}
      </v-btn>
    </div>

    <v-alert
      v-if="forbidden"
      type="warning"
      class="mb-4"
    >
      {{ t("oauth.no_permission") }}
    </v-alert>

    <v-alert
      v-else-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
      <template #append>
        <v-btn
          :color="actionOf('retry').color"
          @click="load"
        >
          {{ t("oauth.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <TableCard
      v-if="!forbidden"
      :headers="headers"
      :items="items"
      :total="total"
      :page="page"
      :ipp="ipp"
      :sort-by="sortBy"
      :search="search"
      :loading="tableLoading"
      table-name="oauth-apps"
      @update:page="setPage"
      @update:ipp="setIpp"
      @update:sort-by="setSortBy"
      @update:search="onSearch"
    >
      <template #no-data>
        <div class="table-fill__empty text-center">
          <div class="text-body-large font-weight-medium mb-1">
            {{ t("oauth.empty") }}
          </div>
          <div class="text-body-medium text-medium-emphasis">
            {{ t("oauth.empty_hint") }}
          </div>
        </div>
      </template>

      <template #[`item.provider`]="{ item }">
        {{ providerLabel(String(item.provider)) }}
      </template>
      <template #[`item.enabled`]="{ item }">
        <v-chip
          :color="item.enabled ? 'success' : 'default'"
        >
          {{ item.enabled ? t("oauth.yes") : t("oauth.no") }}
        </v-chip>
      </template>
      <template #[`item.createdAt`]="{ item }">
        {{ formatWhen(String(item.createdAt)) }}
      </template>
      <template #[`item.actions`]="{ item }">
        <div class="d-flex justify-end ga-1">
          <v-btn
            :icon="mdiCheckDecagram"
            :color="actionOf('verify').color"
            :aria-label="t('oauth.verify')"
            :disabled="mutating"
            @click="verify(item as OauthAppRow)"
          />
          <v-btn
            :icon="mdiPencil"
            :color="actionOf('edit').color"
            :aria-label="t('oauth.edit')"
            @click="openEdit(item as OauthAppRow)"
          />
          <v-btn
            :icon="mdiTrashCan"
            :color="actionOf('delete').color"
            :aria-label="t('oauth.delete')"
            :disabled="mutating"
            @click="onDelete(item as OauthAppRow)"
          />
        </div>
      </template>
    </TableCard>

    <OauthAppFormDialog
      v-model="formOpen"
      :app="editing?.raw"
      :saving="mutating"
      @save="onSave"
    />
  </div>
</template>
