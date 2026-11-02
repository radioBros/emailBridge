<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiContentCopy, mdiOpenInNew } from "@mdi/js";
import type { EbEvent } from "@/api/events";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import EventTypeChip from "@/features/events/components/EventTypeChip.vue";
import { accountLabelOf } from "@/features/events/helpers/accountCache";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";

const props = defineProps<{
  modelValue: boolean;
  detail: EbEvent | null;
  jsonText: string;
  loadError: string | null;
  isFirstLoad: boolean;
  isRefreshing: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
}>();

const { t } = useI18n();
const router = useRouter();
const copyAction = actionOf("copy");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const copy = async (text: string): Promise<void> => {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    /* ignore */
  }
};

const goAccount = (): void => {
  const id = props.detail?.ebaccount;
  if (!id) return;
  void router.push({ path: `/accounts/${id}` });
};

const goDeliveries = (): void => {
  const id = props.detail?.id;
  if (!id) return;
  void router.push({ path: "/deliveries", query: { eventId: id } });
};
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('events.detail_title')"
    :width="480"
  >
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
      shape="form"
    />

    <v-alert
      v-else-if="loadError"
      type="error"
    >
      {{ loadError }}
    </v-alert>

    <div
      v-else-if="detail"
      class="position-relative"
    >
      <AppSectionLoader :loading="isRefreshing" />

      <div class="d-flex flex-wrap align-center ga-2 mb-4">
        <EventTypeChip :type="detail.type" />
        <span class="text-body-small text-medium-emphasis">
          {{ formatDateTime(detail.time) }}
        </span>
      </div>

      <div class="text-body-small text-medium-emphasis mb-1">
        {{ t("events.col_account") }}
      </div>
      <div class="mb-3">
        <v-btn
          v-if="detail.ebaccount"
          :prepend-icon="mdiOpenInNew"
          color="primary"
          class="px-0"
          @click="goAccount"
        >
          {{ accountLabelOf(detail.ebaccount) }}
        </v-btn>
        <span v-else>—</span>
      </div>

      <div class="text-body-small text-medium-emphasis mb-1">
        {{ t("events.col_subject") }}
      </div>
      <div class="mb-3 text-body-medium font-family-monospace">
        {{ detail.subject }}
      </div>

      <div class="d-flex flex-wrap ga-2 mb-4">
        <v-btn
          :color="copyAction.color"
          :prepend-icon="mdiContentCopy"
          @click="copy(jsonText)"
        >
          {{ t("events.copy_json") }}
        </v-btn>
        <v-btn
          :prepend-icon="mdiOpenInNew"
          color="info"
          @click="goDeliveries"
        >
          {{ t("events.jump_deliveries") }}
        </v-btn>
      </div>

      <pre class="events-json pa-3 rounded-lg text-body-small">{{ jsonText }}</pre>
    </div>
  </AppDrawerPanel>
</template>

<style scoped lang="scss">
.events-json {
  background: rgb(var(--v-theme-on-surface), 0.04);
  overflow: auto;
  max-height: 50vh;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
