<script setup lang="ts">
import { computed, onMounted, toRef, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import { useAccountFolders } from "@/features/accounts/composables/useAccountFolders";
import { specialUseIcon } from "@/features/messages/helpers/folders";

const props = defineProps<{ accountId: string }>();

const { t, te } = useI18n();
const router = useRouter();
const retryAction = actionOf("retry");

const accountId = toRef(props, "accountId");
const {
  nodes,
  loadError,
  empty,
  isFirstLoad,
  isRefreshing,
  load,
} = useAccountFolders(accountId);

onMounted(() => {
  void load();
});

watch(accountId, () => {
  void load();
});

const specialUseLabel = (specialUse?: string): string => {
  if (!specialUse) return "";
  const slug = specialUse
    .replace(/[^a-zA-Z0-9]+/g, "_")
    .replace(/^_|_$/g, "")
    .toLowerCase();
  const key = `accounts.folder_special_${slug}`;
  return te(key) ? t(key) : specialUse.replace(/^\\/, "");
};

const statusLabel = (status: string): string => {
  const slug = status.toLowerCase();
  const key = `accounts.folder_status_${slug}`;
  return te(key) ? t(key) : status;
};

const syncModeLabel = (mode?: string): string => {
  if (!mode) return "";
  const slug = mode.toLowerCase();
  const key = `accounts.folder_sync_${slug}`;
  return te(key) ? t(key) : mode;
};

const statusColor = (status: string): string => {
  const s = status.toLowerCase();
  if (s === "ok" || s === "synced" || s === "ready" || s === "active") return "success";
  if (s === "syncing" || s === "pending") return "info";
  if (s === "error" || s === "failed") return "error";
  return "secondary";
};

const formatWhen = (iso?: string): string =>
  iso ? DateUtil.formatDateTime(iso) : EMPTY_VALUE;

const mailboxUnseen = (mailbox: { exists?: number; unseen?: number }): number | undefined =>
  mailbox.unseen;

const countsText = (exists?: number, unseen?: number): string => {
  if (exists == null && unseen == null) return "";
  if (unseen != null && exists != null) {
    return t("accounts.folders_counts", { exists, unseen });
  }
  if (exists != null) return t("accounts.folders_exists", { exists });
  return t("accounts.folders_unseen", { unseen });
};

const openFolder = (mailboxId: string): void => {
  void router.push({
    path: "/messages",
    query: { accountId: props.accountId, mailboxId },
  });
};

const hasNodes = computed(() => nodes.value.length > 0);
</script>

<template>
  <div class="position-relative">
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
      shape="list"
    />

    <v-alert
      v-else-if="loadError"
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

    <div
      v-else-if="empty"
      class="text-body-medium text-medium-emphasis py-6 text-center"
    >
      {{ t("accounts.folders_empty") }}
    </div>

    <div
      v-else-if="hasNodes"
      class="position-relative"
    >
      <v-list
        class="bg-transparent py-0 folder-tree"
      >
        <v-list-item
          v-for="node in nodes"
          :key="node.mailbox.id"
          :prepend-icon="specialUseIcon(node.mailbox.specialUse)"
          :style="{ paddingInlineStart: `${12 + node.depth * 16}px` }"
          rounded="lg"
          class="mb-1 folder-tree__row"
          @click="openFolder(node.mailbox.id)"
        >
          <v-list-item-title class="d-flex flex-wrap align-center ga-2">
            <span class="font-weight-medium">{{ node.label }}</span>
            <v-chip
              v-if="node.mailbox.specialUse"
              color="secondary"
              label
            >
              {{ specialUseLabel(node.mailbox.specialUse) }}
            </v-chip>
            <v-chip
              :color="statusColor(node.mailbox.status)"
              label
            >
              {{ statusLabel(node.mailbox.status) }}
            </v-chip>
          </v-list-item-title>
          <v-list-item-subtitle class="folder-tree__meta text-body-small mt-1">
            <span>{{ countsText(node.mailbox.exists, mailboxUnseen(node.mailbox)) || "—" }}</span>
            <span v-if="node.mailbox.syncMode">
              {{ t("accounts.folders_sync_mode") }}:
              {{ syncModeLabel(node.mailbox.syncMode) }}
            </span>
            <span>
              {{ t("accounts.folders_last_sync") }}:
              {{ formatWhen(node.mailbox.lastSyncAt) }}
            </span>
          </v-list-item-subtitle>
        </v-list-item>
      </v-list>
      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.folder-tree__meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  min-height: 20px;
}

.folder-tree__row {
  min-height: 56px;
}
</style>
