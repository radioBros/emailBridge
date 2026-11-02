<script setup lang="ts">
import { useI18n } from "vue-i18n";
import type { DraftView } from "@/api/messages";
import { formatDateTime } from "@/helpers/Date";
import { formatBytes } from "@/features/messages/helpers/format";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import { mdiFileDocumentOutline } from "@mdi/js";

defineProps<{
  items: DraftView[];
  isFirstLoad: boolean;
  isRefreshing: boolean;
  error: string | null;
}>();

const emit = defineEmits<{
  open: [draftId: string];
}>();

const { t } = useI18n();
</script>

<template>
  <div class="mt-6 position-relative">
    <div class="text-label-large text-medium-emphasis mb-2">
      {{ t("messages.drafts") }}
    </div>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="3"
      shape="list"
    />

    <v-alert
      v-else-if="error"
      type="error"
    >
      {{ error }}
    </v-alert>

    <div
      v-else-if="!items.length"
      class="text-body-medium text-medium-emphasis"
    >
      {{ t("messages.drafts_empty") }}
    </div>

    <div
      v-else
      class="position-relative"
    >
      <AppSectionLoader :status="isRefreshing" />
      <v-list
        class="bg-transparent py-0"
      >
        <v-list-item
          v-for="d in items"
          :key="d.id"
          :prepend-icon="mdiFileDocumentOutline"
          :title="d.subject || t('messages.no_subject')"
          :subtitle="`${formatDateTime(d.updatedAt)} · ${formatBytes(d.size)}`"
          rounded="lg"
          @click="emit('open', d.id)"
        />
      </v-list>
    </div>
  </div>
</template>
