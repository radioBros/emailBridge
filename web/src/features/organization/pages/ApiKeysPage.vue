<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiPlus } from "@mdi/js";
import StatusChip from "@/components/common/StatusChip.vue";
import TableCard from "@/components/tables/TableCard.vue";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import { useApiKeys, type ApiKeyRow } from "@/features/organization/composables/useApiKeys";
import { useOrgRole } from "@/features/organization/composables/useOrgRole";
import CreateApiKeyDialog from "@/features/organization/components/CreateApiKeyDialog.vue";
import EditApiKeyDialog from "@/features/organization/components/EditApiKeyDialog.vue";
import TokenOnceDialog from "@/features/organization/components/TokenOnceDialog.vue";
import PermissionState from "@/features/organization/components/PermissionState.vue";
import type { ApiKeyCreateBody, ApiKeyPatchBody } from "@/api/apikeys";

const { t, locale } = useI18n();
const confirm = useConfirm();
const org = useOrgRole();
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
  createdToken,
  tableLoading,
  load,
  create,
  update,
  revoke,
  clearCreatedToken,
  onSearch,
} = useApiKeys();

const createOpen = ref(false);
const tokenOpen = ref(false);
const editOpen = ref(false);
const editing = ref<ApiKeyRow | null>(null);

onMounted(() => {
  void load();
});

watch(createdToken, (v) => {
  if (v) {
    createOpen.value = false;
    tokenOpen.value = true;
  }
});

watch(tokenOpen, (v) => {
  if (!v) clearCreatedToken();
});

const headers = computed(() => [
  { key: "name", title: t("organization.api_keys.col_name"), sortable: true },
  { key: "prefix", title: t("organization.api_keys.col_prefix"), sortable: false },
  { key: "scopes", title: t("organization.api_keys.col_scopes"), sortable: false },
  { key: "accountName", title: t("organization.api_keys.col_account"), sortable: false },
  { key: "lastUsedAt", title: t("organization.api_keys.col_last_used"), sortable: true },
  // Default sort key: a header of its own so the phone sort select shows a label, not "createdAt".
  { key: "createdAt", title: t("organization.api_keys.col_created"), sortable: true },
  { key: "expiresAt", title: t("organization.api_keys.col_expires"), sortable: false },
  { key: "actions", title: t("organization.actions"), sortable: false, forced: true },
]);

const formatWhen = (iso: string | null): string => {
  if (!iso) return EMPTY_VALUE;
  return (
    DateUtil.parse(iso)?.setLocale(locale.value).toFormat("dd LLL yyyy HH:mm") ?? iso
  );
};

const onCreate = async (body: ApiKeyCreateBody): Promise<void> => {
  await create(body);
};

const openEdit = (row: ApiKeyRow): void => {
  editing.value = row;
  editOpen.value = true;
};

const onEdit = async (body: ApiKeyPatchBody): Promise<void> => {
  if (!editing.value) return;
  const ok = await update(editing.value, body);
  if (ok) {
    editOpen.value = false;
    editing.value = null;
  }
};

const onRevoke = async (row: ApiKeyRow): Promise<void> => {
  const ok = await confirm({
    title: t("organization.api_keys.revoke_title"),
    body: t("organization.api_keys.revoke_body", { name: row.name }),
    variant: "danger",
    confirmText: t("organization.api_keys.revoke"),
  });
  if (!ok) return;
  await revoke(row);
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-4 mb-6">
      <div class="min-width-0">
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("organization.api_keys.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("organization.api_keys.subtitle") }}
        </p>
      </div>
      <v-btn
        v-if="org.canWriteApiKeys.value && !forbidden"
        :color="actionOf('create').color"
        :prepend-icon="mdiPlus"
        class="flex-shrink-0"
        @click="createOpen = true"
      >
        {{ t("organization.api_keys.create") }}
      </v-btn>
    </div>

    <PermissionState
      v-if="forbidden"
      :title="t('organization.no_permission_title')"
      :body="t('organization.api_keys.no_permission')"
    />

    <template v-else>
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
            {{ t("organization.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <TableCard
        v-model:page="page"
        v-model:ipp="ipp"
        v-model:sort-by="sortBy"
        :search="search"
        :headers="headers"
        :items="items"
        :total="total"
        :loading="tableLoading"
        table-name="api-keys"
        item-value="id"
        @update:search="onSearch"
      >
        <template #no-data>
          <div class="d-flex flex-column align-center text-center py-6 ga-3">
            <v-avatar-hero
              color="success"
            >
              <v-icon
                :icon="mdiPlus"
              />
            </v-avatar-hero>
            <p class="text-body-medium text-medium-emphasis mb-0">
              {{ t("organization.api_keys.empty") }}
            </p>
            <v-btn
              v-if="org.canWriteApiKeys.value"
              :color="actionOf('create').color"
              :prepend-icon="mdiPlus"
              @click="createOpen = true"
            >
              {{ t("organization.api_keys.create") }}
            </v-btn>
          </div>
        </template>

        <template #[`item.scopes`]="{ item }">
          <div class="d-flex flex-wrap ga-1 py-1">
            <StatusChip
              v-for="scope in item.scopes.slice(0, 4)"
              :key="scope"
              :label="scope"
              color="info"
            />
            <v-chip
              v-if="item.scopes.length > 4"
            >
              +{{ item.scopes.length - 4 }}
            </v-chip>
          </div>
        </template>

        <template #[`item.accountName`]="{ item }">
          {{ item.accountName || EMPTY_VALUE }}
        </template>

        <template #[`item.lastUsedAt`]="{ item }">
          {{ formatWhen(item.lastUsedAt) }}
        </template>

        <template #[`item.createdAt`]="{ item }">
          {{ formatWhen(item.createdAt) }}
        </template>

        <template #[`item.expiresAt`]="{ item }">
          {{ formatWhen(item.expiresAt) }}
        </template>

        <template #[`item.actions`]="{ item }">
          <div class="d-flex ga-1 justify-end">
            <v-btn
              v-tippy="t('organization.api_keys.edit')"
              :icon="actionOf('edit').icon"
              :color="actionOf('edit').color"
              :disabled="!org.canWriteApiKeys.value"
              :aria-label="t('organization.api_keys.edit')"
              @click.stop="openEdit(item)"
            />
            <v-btn
              v-tippy="t('organization.api_keys.revoke')"
              :icon="actionOf('revoke').icon"
              :color="actionOf('revoke').color"
              :disabled="!org.canWriteApiKeys.value"
              :aria-label="t('organization.api_keys.revoke')"
              @click.stop="onRevoke(item)"
            />
          </div>
        </template>
      </TableCard>
    </template>

    <CreateApiKeyDialog
      v-model:open="createOpen"
      :loading="mutating"
      @submit="onCreate"
    />
    <EditApiKeyDialog
      v-model:open="editOpen"
      :api-key="editing"
      :loading="mutating"
      @submit="onEdit"
    />
    <TokenOnceDialog
      v-model:open="tokenOpen"
      :created="createdToken"
    />
  </div>
</template>
