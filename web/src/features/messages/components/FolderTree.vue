<script setup lang="ts">
import { useI18n } from "vue-i18n";
import type { FolderNode } from "@/features/messages/helpers/folders";
import { specialUseIcon } from "@/features/messages/helpers/folders";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";

defineProps<{
  nodes: FolderNode[];
  selectedId: string;
  isFirstLoad: boolean;
  isRefreshing: boolean;
  error: string | null;
  empty: boolean;
}>();

const emit = defineEmits<{
  select: [mailboxId: string];
}>();

const { t } = useI18n();
</script>

<template>
  <div class="position-relative messages-folder-tree">
    <div class="text-label-large text-medium-emphasis mb-2">
      {{ t("messages.folders") }}
    </div>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="5"
      shape="list"
    />

    <v-alert
      v-else-if="error"
      type="error"
    >
      {{ error }}
    </v-alert>

    <div
      v-else-if="empty"
      class="text-body-medium text-medium-emphasis"
    >
      {{ t("messages.folder_empty") }}
    </div>

    <div
      v-else
      class="position-relative"
    >
      <AppSectionLoader :status="isRefreshing" />
      <v-list
        class="bg-transparent py-0"
        nav
      >
        <v-list-item
          v-for="node in nodes"
          :key="node.mailbox.id"
          :active="node.mailbox.id === selectedId"
          :prepend-icon="specialUseIcon(node.mailbox.specialUse)"
          :style="{ paddingInlineStart: `${8 + node.depth * 16}px` }"
          :title="node.label"
          :subtitle="node.mailbox.exists != null ? String(node.mailbox.exists) : undefined"
          rounded="lg"
          color="primary"
          @click="emit('select', node.mailbox.id)"
        />
      </v-list>
    </div>
  </div>
</template>
