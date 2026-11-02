<script setup lang="ts">
import { computed, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiDownload, mdiShieldSearch } from "@mdi/js";
import type { Receipt } from "@/api/pec";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import PecSignatureBadge from "@/features/pec/components/PecSignatureBadge.vue";
import PecTypeChip from "@/features/pec/components/PecTypeChip.vue";
import { usePecReceiptDetail } from "@/features/pec/composables/usePecReceiptDetail";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const props = defineProps<{
  modelValue: boolean;
  receiptId: string | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  "open-outbound": [outboundId: string];
}>();

const { t, te } = useI18n();
const downloadAction = actionOf("download");
const verifyAction = actionOf("verify");

const {
  open,
  receipt,
  loadError,
  evidence,
  evidenceError,
  evidenceMissing,
  verifying,
  downloading,
  downloadError,
  isFirstLoad,
  isRefreshing,
  show,
  close,
  verifyEvidence,
  downloadRaw,
} = usePecReceiptDetail();

watch(
  () => [props.modelValue, props.receiptId] as const,
  ([isOpen, id]) => {
    if (isOpen && id) {
      void show(id);
    } else if (!isOpen) {
      close();
    }
  },
  { immediate: true },
);

watch(open, (v) => {
  if (!v) emit("update:modelValue", false);
});

const drawerOpen = computed({
  get: () => props.modelValue,
  set: (v: boolean) => {
    emit("update:modelValue", v);
    if (!v) close();
  },
});

const formatWhen = (iso?: string): string =>
  iso ? DateUtil.formatDateTime(iso) : EMPTY_VALUE;

const recipientKindLabel = (kind: string): string => {
  if (kind === "certificato") return t("pec.recipient_certified");
  if (kind === "esterno") return t("pec.recipient_ordinary");
  return kind;
};

const onOutbound = (r: Receipt): void => {
  if (r.outboundId) emit("open-outbound", r.outboundId);
};

const evidenceMsg = computed(() => {
  if (downloadError.value?.startsWith("pec.") && te(downloadError.value)) {
    return t(downloadError.value);
  }
  return downloadError.value;
});
</script>

<template>
  <AppDrawerPanel
    v-model="drawerOpen"
    :title="t('pec.detail_title')"
    :width="440"
  >
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="8"
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
      v-else-if="receipt"
      class="position-relative pa-4"
    >
      <AppSectionLoader :status="isRefreshing" />

      <div class="d-flex align-center flex-wrap ga-2 mb-4">
        <PecTypeChip :type="receipt.type" />
        <PecSignatureBadge :signature="receipt.signature" />
      </div>

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("pec.col_at") }}
      </div>
      <div class="text-body-medium mb-4">
        {{ formatWhen(receipt.at) }}
      </div>

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("pec.col_transport") }}
      </div>
      <div class="text-body-medium font-family-monospace text-break mb-4">
        {{ receipt.transportId || EMPTY_VALUE }}
      </div>

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("pec.col_recipient") }}
      </div>
      <div class="text-body-medium mb-4">
        {{ receipt.deliveredTo || EMPTY_VALUE }}
      </div>

      <div
        v-if="receipt.outboundId"
        class="mb-4"
      >
        <div class="text-label-large text-medium-emphasis mb-1">
          {{ t("pec.col_outbound") }}
        </div>
        <v-btn-inline
          color="primary"
          @click="onOutbound(receipt)"
        >
          {{ receipt.outboundId }}
        </v-btn-inline>
      </div>

      <div class="text-subtitle-2 font-weight-medium mb-2">
        {{ t("pec.certification") }}
      </div>
      <div class="mb-4">
        <template v-if="receipt.sender">
          <div class="text-body-small text-medium-emphasis mb-1">
            {{ t("pec.sender") }}
          </div>
          <div class="text-body-medium mb-3">
            {{ receipt.sender }}
          </div>
        </template>
        <template v-if="receipt.provider">
          <div class="text-body-small text-medium-emphasis mb-1">
            {{ t("pec.provider") }}
          </div>
          <div class="text-body-medium mb-3">
            {{ receipt.provider }}
          </div>
        </template>
        <template v-if="receipt.receiptKind">
          <div class="text-body-small text-medium-emphasis mb-1">
            {{ t("pec.receipt_kind") }}
          </div>
          <div class="text-body-medium mb-3">
            {{ receipt.receiptKind }}
          </div>
        </template>
        <template v-if="receipt.error">
          <div class="text-body-small text-medium-emphasis mb-1">
            {{ t("pec.error") }}
          </div>
          <div class="text-body-medium mb-3">
            {{ receipt.error }}
            <span v-if="receipt.errorDetail"> — {{ receipt.errorDetail }}</span>
          </div>
        </template>
        <template v-if="receipt.refMessageIdHeader">
          <div class="text-body-small text-medium-emphasis mb-1">
            {{ t("pec.ref_message_id") }}
          </div>
          <div class="text-body-medium font-family-monospace text-break mb-3">
            {{ receipt.refMessageIdHeader }}
          </div>
        </template>
      </div>

      <div class="text-subtitle-2 font-weight-medium mb-2">
        {{ t("pec.recipients") }}
      </div>
      <div
        v-if="!(receipt.recipients?.length)"
        class="text-body-medium text-medium-emphasis mb-4"
      >
        {{ EMPTY_VALUE }}
      </div>
      <v-list
        v-else
        class="mb-4 bg-transparent"
      >
        <v-list-item
          v-for="(rcpt, idx) in receipt.recipients"
          :key="`${rcpt.address}-${idx}`"
        >
          <v-list-item-title>{{ rcpt.address }}</v-list-item-title>
          <v-list-item-subtitle>{{ recipientKindLabel(rcpt.kind) }}</v-list-item-subtitle>
        </v-list-item>
      </v-list>

      <div class="text-subtitle-2 font-weight-medium mb-2">
        {{ t("pec.signature_details") }}
      </div>
      <v-list
        class="mb-4 bg-transparent"
      >
        <v-list-item v-if="receipt.signature.signer">
          <v-list-item-title class="text-medium-emphasis">
            {{ t("pec.signer") }}
          </v-list-item-title>
          <v-list-item-subtitle>{{ receipt.signature.signer }}</v-list-item-subtitle>
        </v-list-item>
        <v-list-item v-if="receipt.signature.issuer">
          <v-list-item-title class="text-medium-emphasis">
            {{ t("pec.issuer") }}
          </v-list-item-title>
          <v-list-item-subtitle>{{ receipt.signature.issuer }}</v-list-item-subtitle>
        </v-list-item>
        <v-list-item>
          <v-list-item-title class="text-medium-emphasis">
            {{ t("pec.sha256") }}
          </v-list-item-title>
          <v-list-item-subtitle class="font-family-monospace text-break">
            {{ receipt.sha256 }}
          </v-list-item-subtitle>
        </v-list-item>
      </v-list>

      <div class="d-flex flex-wrap ga-2 mb-4">
        <v-btn
          :color="downloadAction.color"
          :prepend-icon="mdiDownload"
          :loading="downloading"
          @click="downloadRaw"
        >
          {{ t("pec.download_raw") }}
        </v-btn>
        <v-btn
          :color="verifyAction.color"
          :prepend-icon="mdiShieldSearch"
          :loading="verifying"
          @click="verifyEvidence"
        >
          {{ t("pec.verify_evidence") }}
        </v-btn>
      </div>

      <v-alert
        v-if="evidenceMsg"
        type="error"
        class="mb-4"
      >
        {{ evidenceMsg }}
      </v-alert>

      <v-alert
        v-if="evidenceMissing"
        type="info"
        class="mb-4"
      >
        {{ t("pec.evidence_none") }}
      </v-alert>

      <v-alert
        v-if="evidenceError"
        type="error"
        class="mb-4"
      >
        {{ evidenceError }}
      </v-alert>

      <AppCard
        v-if="evidence"
        class="mb-2"
      >
        <template #header>
          <span class="text-title-small font-weight-bold">
            {{ t("pec.evidence_title") }}
          </span>
        </template>
        <div class="d-flex flex-wrap ga-2 mb-3">
          <v-chip
            :color="evidence.verification.intact ? 'success' : 'error'"
            label
          >
            {{
              evidence.verification.intact
                ? t("pec.evidence_intact")
                : t("pec.evidence_not_intact")
            }}
          </v-chip>
          <v-chip
            :color="evidence.verification.hashMatches ? 'success' : 'error'"
            label
          >
            {{
              evidence.verification.hashMatches
                ? t("pec.evidence_hash_ok")
                : t("pec.evidence_hash_bad")
            }}
          </v-chip>
          <v-chip
            :color="evidence.verification.locked ? 'info' : 'grey'"
            label
          >
            {{
              evidence.verification.locked
                ? t("pec.evidence_locked")
                : t("pec.evidence_unlocked")
            }}
          </v-chip>
        </div>
        <div
          v-if="evidence.verification.lockedUntil"
          class="text-body-small mb-2"
        >
          {{ t("pec.evidence_lock_until") }}:
          {{ formatWhen(evidence.verification.lockedUntil) }}
        </div>
        <div
          v-if="evidence.verification.tsa"
          class="text-body-small"
        >
          {{ t("pec.evidence_tsa") }}:
          <span :class="evidence.verification.tsa.valid ? 'text-success' : 'text-error'">
            {{
              evidence.verification.tsa.valid
                ? t("pec.evidence_tsa_valid")
                : t("pec.evidence_tsa_invalid")
            }}
          </span>
          <span
            v-if="evidence.verification.tsa.at"
            class="text-medium-emphasis"
          >
            · {{ formatWhen(evidence.verification.tsa.at) }}
          </span>
        </div>
      </AppCard>
    </div>
  </AppDrawerPanel>
</template>
