<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiAccountPlus } from "@mdi/js";
import StatusChip from "@/components/common/StatusChip.vue";
import TableCard from "@/components/tables/TableCard.vue";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import { useMembers, type MemberRow } from "@/features/organization/composables/useMembers";
import { useOrgRole } from "@/features/organization/composables/useOrgRole";
import { roleColor } from "@/features/organization/helpers/roles";
import InviteMemberDialog from "@/features/organization/components/InviteMemberDialog.vue";
import EditMemberRoleDialog from "@/features/organization/components/EditMemberRoleDialog.vue";
import PermissionState from "@/features/organization/components/PermissionState.vue";

const { t, locale } = useI18n();
const { mobile } = useDisplay();
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
  tableLoading,
  load,
  invite,
  changeRole,
  remove,
  onSearch,
} = useMembers();

const inviteOpen = ref(false);
const editOpen = ref(false);
const editing = ref<MemberRow | null>(null);

onMounted(() => {
  void load();
});

const headers = computed(() => {
  const cols = [
    { key: "name", title: t("organization.members.col_name"), sortable: false },
    ...(mobile.value
      ? []
      : [{ key: "email", title: t("organization.members.col_email"), sortable: true }]),
    { key: "role", title: t("organization.members.col_role"), sortable: true },
    { key: "lastLoginAt", title: t("organization.members.col_last_login"), sortable: false },
    { key: "actions", title: t("organization.actions"), sortable: false, forced: true },
  ];
  return cols;
});

const formatLogin = (iso: string | null): string => {
  if (!iso) return EMPTY_VALUE;
  return (
    DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ??
    DateUtil.formatDateTime(iso)
  );
};

const onInvite = async (payload: { email: string; role: string }): Promise<void> => {
  const ok = await invite(payload.email, payload.role);
  if (ok) inviteOpen.value = false;
};

const openEdit = (row: MemberRow): void => {
  editing.value = row;
  editOpen.value = true;
};

const onEditRole = async (role: string): Promise<void> => {
  if (!editing.value) return;
  const ok = await changeRole(editing.value, role);
  if (ok) {
    editOpen.value = false;
    editing.value = null;
  }
};

const onRemove = async (row: MemberRow): Promise<void> => {
  const ok = await confirm({
    title: t("organization.members.remove_title"),
    body: t("organization.members.remove_body", { email: row.email || row.name }),
    variant: "danger",
    confirmText: t("organization.members.remove"),
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
          {{ t("organization.members.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("organization.members.subtitle") }}
        </p>
      </div>
      <v-btn
        v-if="org.canWriteMembers.value && !forbidden"
        :color="actionOf('invite').color"
        :prepend-icon="mdiAccountPlus"
        class="flex-shrink-0"
        @click="inviteOpen = true"
      >
        {{ t("organization.members.invite") }}
      </v-btn>
    </div>

    <PermissionState
      v-if="forbidden"
      :title="t('organization.no_permission_title')"
      :body="t('organization.members.no_permission')"
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
        table-name="members"
        item-value="id"
        @update:search="onSearch"
      >
        <template #no-data>
          <div class="d-flex flex-column align-center text-center py-6 ga-3">
            <v-avatar-hero
              color="success"
            >
              <v-icon
                :icon="mdiAccountPlus"
              />
            </v-avatar-hero>
            <p class="text-body-medium text-medium-emphasis mb-0">
              {{ t("organization.members.empty") }}
            </p>
            <v-btn
              v-if="org.canWriteMembers.value"
              :color="actionOf('invite').color"
              :prepend-icon="mdiAccountPlus"
              @click="inviteOpen = true"
            >
              {{ t("organization.members.invite") }}
            </v-btn>
          </div>
        </template>

        <template #[`item.name`]="{ item }">
          <div class="font-weight-medium">
            {{ item.name }}
          </div>
          <div
            v-if="mobile && item.email"
            class="text-body-small text-medium-emphasis"
          >
            {{ item.email }}
          </div>
        </template>

        <template #[`item.role`]="{ item }">
          <StatusChip
            :color="roleColor(item.role)"
            :label="t(`organization.roles.${item.role}`)"
          />
          <v-chip
            v-if="item.pending"
            class="ms-1"
            color="warning"
          >
            {{ t("organization.members.pending") }}
          </v-chip>
        </template>

        <template #[`item.lastLoginAt`]="{ item }">
          {{ formatLogin(item.lastLoginAt) }}
        </template>

        <template #[`item.actions`]="{ item }">
          <div class="d-flex ga-1 justify-end">
            <v-btn
              v-tippy="t('organization.members.edit_role')"
              :icon="actionOf('edit').icon"
              :color="actionOf('edit').color"
              :disabled="!org.canEditMember(item.role)"
              :aria-label="t('organization.members.edit_role')"
              @click.stop="openEdit(item)"
            />
            <v-btn
              v-tippy="t('organization.members.remove')"
              :icon="actionOf('remove').icon"
              :color="actionOf('remove').color"
              :disabled="!org.canEditMember(item.role)"
              :aria-label="t('organization.members.remove')"
              @click.stop="onRemove(item)"
            />
          </div>
        </template>
      </TableCard>
    </template>

    <InviteMemberDialog
      v-model:open="inviteOpen"
      :roles="org.assignableRoles.value"
      :loading="mutating"
      @submit="onInvite"
    />
    <EditMemberRoleDialog
      v-model:open="editOpen"
      :member="editing"
      :roles="org.assignableRoles.value"
      :loading="mutating"
      @submit="onEditRole"
    />
  </div>
</template>
