<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiContentCopy } from "@mdi/js";
import type { Delivery } from "@/api/deliveries";
import DeliveryStateChip from "@/features/deliveries/components/DeliveryStateChip.vue";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import {
  accountLabelOf,
  endpointLabelOf,
  routeLabelOf,
} from "@/features/deliveries/helpers/labelCache";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const props = defineProps<{
  modelValue: boolean;
  detail: Delivery | null;
  loadError: string | null;
  isFirstLoad: boolean;
  isRefreshing: boolean;
  canReplay: boolean;
  replaying: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  replay: [];
}>();

const { t } = useI18n();
const replayAction = actionOf("replay");
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

const attempts = computed(() => props.detail?.attempts ?? []);
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('deliveries.detail_title')"
    :width="440"
  >
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
      class="pa-4"
      shape="form"
    />

    <div
      v-else-if="loadError"
      class="pa-4"
    >
      <v-alert
        type="error"
      >
        {{ loadError }}
      </v-alert>
    </div>

    <div
      v-else-if="detail"
      class="pa-4 position-relative"
    >
      <AppSectionLoader :loading="isRefreshing" />

      <div class="d-flex flex-wrap align-center ga-1 mb-4">
        <DeliveryStateChip :state="detail.state" />
        <v-chip
          v-if="detail.replayedAt"
          color="secondary"
          label
        >
          {{ t("deliveries.chip_replayed") }}
        </v-chip>
      </div>

      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("deliveries.col_created") }}
      </div>
      <div class="mb-3">
        {{ formatDateTime(detail.createdAt) }}
      </div>

      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("deliveries.col_event") }}
      </div>
      <div class="mb-3">
        {{ detail.eventType }}
      </div>

      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("deliveries.event_id") }}
      </div>
      <div class="d-flex align-center ga-1 mb-3">
        <code class="text-body-small">{{ detail.eventId }}</code>
        <v-btn-affix
          :icon="mdiContentCopy"
          :aria-label="t('deliveries.copy')"
          :color="copyAction.color"
          @click="copy(detail.eventId)"
        />
      </div>

      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("deliveries.webhook_id") }}
      </div>
      <div class="d-flex align-center ga-1 mb-3">
        <code class="text-body-small">{{ detail.eventId }}</code>
        <v-btn-affix
          :icon="mdiContentCopy"
          :aria-label="t('deliveries.copy')"
          :color="copyAction.color"
          @click="copy(detail.eventId)"
        />
      </div>

      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("deliveries.col_endpoint") }}
      </div>
      <div class="mb-3">
        {{ endpointLabelOf(detail.endpointId) }}
      </div>

      <div
        v-if="detail.routeId"
        class="text-caption text-medium-emphasis mb-1"
      >
        {{ t("deliveries.col_route") }}
      </div>
      <div
        v-if="detail.routeId"
        class="mb-3"
      >
        {{ routeLabelOf(detail.routeId) }}
      </div>

      <div
        v-if="detail.accountId"
        class="text-caption text-medium-emphasis mb-1"
      >
        {{ t("deliveries.col_account") }}
      </div>
      <div
        v-if="detail.accountId"
        class="mb-4"
      >
        {{ accountLabelOf(detail.accountId) }}
      </div>

      <div class="text-subtitle-2 font-weight-bold mb-2">
        {{ t("deliveries.attempts_timeline") }}
      </div>
      <div
        v-if="!attempts.length"
        class="text-body-medium text-medium-emphasis"
      >
        {{ t("deliveries.no_attempts") }}
      </div>
      <v-timeline
        v-else
        side="end"
        truncate-line="both"
      >
        <v-timeline-item
          v-for="(a, i) in attempts"
          :key="i"
          :dot-color="a.statusCode && a.statusCode < 400 ? 'success' : 'error'"
        >
          <div class="text-body-medium">
            {{ formatDateTime(a.at) }}
            <span
              v-if="a.statusCode"
              class="ms-2"
            >HTTP {{ a.statusCode }}</span>
          </div>
          <div class="text-caption text-medium-emphasis">
            {{ t("deliveries.ms", { n: a.latencyMs }) }}
            <span v-if="a.error"> · {{ a.error }}</span>
          </div>
          <div
            v-if="a.responseExcerpt"
            class="text-caption mt-1 text-wrap"
          >
            {{ a.responseExcerpt }}
          </div>
        </v-timeline-item>
      </v-timeline>
    </div>

    <template
      v-if="canReplay && detail"
      #footer
    >
      <v-btn
        :color="replayAction.color"
        :prepend-icon="replayAction.icon"
        :loading="replaying"
        @click="emit('replay')"
      >
        {{ t("deliveries.replay") }}
      </v-btn>
    </template>
  </AppDrawerPanel>
</template>
