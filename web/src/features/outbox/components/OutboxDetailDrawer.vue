<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import type { SubmitView } from "@/api/outbox";
import OutboundPecBlock from "@/features/pec/components/OutboundPecBlock.vue";
import OutboundStateChip from "@/features/outbox/components/OutboundStateChip.vue";
import {
  attemptKindLabelKey,
  attemptStageLabelKey,
  copyToSentLabelKey,
} from "@/features/outbox/helpers/labels";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const props = defineProps<{
  modelValue: boolean;
  detail: SubmitView | null;
  loadError: string | null;
  isFirstLoad: boolean;
  isRefreshing: boolean;
  canCancel: boolean;
  cancelling: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  cancel: [];
}>();

const { t, te } = useI18n();
const cancelAction = actionOf("cancel");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const policyLabel = computed(() => {
  const p = props.detail?.policy;
  if (p === "atMostOnce") return t("outbox.policy_atMostOnce");
  if (p === "standard") return t("outbox.policy_standard");
  return p ?? "—";
});

const showOutcomeNote = computed(() => props.detail?.state === "outcomeUnknown");

const copyStateLabel = computed(() => {
  const state = props.detail?.copyToSent.state ?? "";
  const key = copyToSentLabelKey(state);
  return key && te(key) ? t(key) : state || "—";
});

const stageLabel = (stage: string): string => {
  const key = attemptStageLabelKey(stage);
  return key && te(key) ? t(key) : stage;
};

const kindLabel = (kind: string): string => {
  const key = attemptKindLabelKey(kind);
  return key && te(key) ? t(key) : kind;
};
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('outbox.detail_title')"
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
      class="position-relative pa-4"
    >
      <AppSectionLoader :status="isRefreshing" />

      <div class="d-flex align-center flex-wrap ga-2 mb-4">
        <OutboundStateChip :state="detail.state" />
        <span class="text-body-small text-medium-emphasis">
          {{ formatDateTime(detail.createdAt) }}
        </span>
      </div>

      <v-alert
        v-if="showOutcomeNote"
        class="mb-4"
        type="warning"
      >
        {{ t("outbox.outcome_unknown_note") }}
      </v-alert>

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("outbox.col_subject") }}
      </div>
      <div class="text-body-large mb-4">
        {{ detail.subject || "—" }}
      </div>

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("outbox.message_id") }}
      </div>
      <div class="text-body-medium font-family-monospace mb-4 text-break">
        {{ detail.messageIdHeader || detail.messageId }}
      </div>

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("outbox.policy") }}
      </div>
      <div class="text-body-medium mb-4">
        {{ policyLabel }}
      </div>

      <div class="text-subtitle-2 font-weight-bold mb-2">
        {{ t("outbox.envelope") }}
      </div>
      <v-list
        class="mb-4 bg-transparent"
      >
        <v-list-item>
          <v-list-item-title class="text-medium-emphasis">
            {{ t("outbox.envelope_from") }}
          </v-list-item-title>
          <v-list-item-subtitle>{{ detail.envelope.from }}</v-list-item-subtitle>
        </v-list-item>
        <v-list-item>
          <v-list-item-title class="text-medium-emphasis">
            {{ t("outbox.envelope_to") }}
          </v-list-item-title>
          <v-list-item-subtitle>
            {{ (detail.envelope.to ?? []).join(", ") || "—" }}
          </v-list-item-subtitle>
        </v-list-item>
        <v-list-item v-if="detail.envelope.cc?.length">
          <v-list-item-title class="text-medium-emphasis">
            {{ t("outbox.envelope_cc") }}
          </v-list-item-title>
          <v-list-item-subtitle>
            {{ detail.envelope.cc.join(", ") }}
          </v-list-item-subtitle>
        </v-list-item>
      </v-list>

      <div class="text-subtitle-2 font-weight-bold mb-2">
        {{ t("outbox.copy_to_sent") }}
      </div>
      <div class="text-body-medium mb-1">
        {{ detail.copyToSent.enabled ? t("outbox.copy_enabled") : t("outbox.copy_disabled") }}
        · {{ copyStateLabel }}
      </div>
      <div
        v-if="detail.copyToSent.path"
        class="text-body-small text-medium-emphasis mb-4"
      >
        {{ t("outbox.copy_path") }}: {{ detail.copyToSent.path }}
      </div>

      <OutboundPecBlock
        v-if="detail.pec"
        :pec="detail.pec"
      />

      <div class="text-subtitle-2 font-weight-bold mb-2">
        {{ t("outbox.attempts_timeline") }}
      </div>
      <div
        v-if="!(detail.attempts?.length)"
        class="text-body-medium text-medium-emphasis mb-4"
      >
        {{ t("outbox.no_attempts") }}
      </div>
      <v-timeline
        v-else
        class="mb-4"
        side="end"
        truncate-line="both"
      >
        <v-timeline-item
          v-for="(attempt, idx) in detail.attempts"
          :key="`${attempt.at}-${idx}`"
          dot-color="info"
        >
          <div class="text-body-small text-medium-emphasis">
            {{ formatDateTime(attempt.at) }}
          </div>
          <div class="d-flex align-center flex-wrap ga-2 text-body-medium font-weight-medium">
            <span>{{ stageLabel(attempt.stage) }}</span>
            <v-chip
              v-if="attempt.code != null"
              class="font-family-monospace"
            >
              {{ attempt.code }}
            </v-chip>
            <span
              v-if="attempt.kind"
              class="text-medium-emphasis"
            >
              {{ kindLabel(attempt.kind) }}
            </span>
          </div>
          <div
            v-if="attempt.latencyMs != null"
            class="text-body-small"
          >
            {{ t("outbox.ms", { n: attempt.latencyMs }) }}
          </div>
          <div
            v-if="attempt.response"
            class="text-body-small text-medium-emphasis text-break"
          >
            {{ attempt.response }}
          </div>
        </v-timeline-item>
      </v-timeline>

      <v-alert
        v-if="detail.finalError"
        class="mb-4"
        type="error"
      >
        <div class="font-weight-medium mb-1">
          {{ t("outbox.final_error") }}
        </div>
        <div>{{ detail.finalError.detail || detail.finalError.title }}</div>
        <div
          v-if="detail.finalError.code"
          class="text-caption mt-1"
        >
          {{ detail.finalError.code }}
        </div>
      </v-alert>
    </div>

    <template
      v-if="canCancel && detail"
      #footer
    >
      <v-btn
        :color="cancelAction.color"
        :prepend-icon="cancelAction.icon"
        :loading="cancelling"
        @click="emit('cancel')"
      >
        {{ t("outbox.cancel") }}
      </v-btn>
    </template>
  </AppDrawerPanel>
</template>
