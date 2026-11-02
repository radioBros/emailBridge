<script setup lang="ts">
import { mdiDelete, mdiLanConnect, mdiPencil, mdiPlayCircle } from "@mdi/js";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import AccountSyncButton from "@/features/accounts/components/AccountSyncButton.vue";
import {
  canDelete,
  canEdit,
  canReconnect,
  canResume,
  canSync,
} from "@/features/accounts/helpers/accountState";

const props = defineProps<{
  state: string;
  accountId: string;
  loading?: boolean;
}>();

const emit = defineEmits<{
  sync: [mode: "fast" | "full"];
  reconnect: [];
  resume: [];
  delete: [];
}>();

const { t } = useI18n();
const editAction = actionOf("edit");
const deleteAction = actionOf("delete");
const reconnectAction = actionOf("reconnect");
const resumeAction = actionOf("resume");
</script>

<template>
  <div
    class="d-flex flex-wrap align-center ga-1"
    @click.stop
  >
    <AccountSyncButton
      v-if="canSync(props.state)"
      :loading="loading"
      @sync="(mode) => emit('sync', mode)"
    />
    <v-btn
      v-if="canReconnect(props.state)"
      v-tippy="t('accounts.action_reconnect')"
      :icon="mdiLanConnect"
      :color="reconnectAction.color"
      :loading="loading"
      :aria-label="t('accounts.action_reconnect')"
      @click="emit('reconnect')"
    />
    <v-btn
      v-if="canResume(props.state)"
      v-tippy="t('accounts.action_resume')"
      :icon="mdiPlayCircle"
      :color="resumeAction.color"
      :loading="loading"
      :aria-label="t('accounts.action_resume')"
      @click="emit('resume')"
    />
    <v-btn
      v-if="canEdit(props.state)"
      v-tippy="t('accounts.action_edit')"
      :icon="mdiPencil"
      :color="editAction.color"
      :aria-label="t('accounts.action_edit')"
      :to="`/accounts/${accountId}/edit`"
    />
    <v-btn
      v-if="canDelete(props.state)"
      v-tippy="t('accounts.action_delete')"
      :icon="mdiDelete"
      :color="deleteAction.color"
      :loading="loading"
      :aria-label="t('accounts.action_delete')"
      @click="emit('delete')"
    />
  </div>
</template>
