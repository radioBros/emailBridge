<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import type { OutboundPECStatus } from "@/api/pec";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import PecReceiptDrawer from "@/features/pec/components/PecReceiptDrawer.vue";
import {
  pecDeliveryColor,
  pecDeliveryLabelKey,
  pecOutboundLabelKey,
  pecOutboundStatusOf,
} from "@/features/pec/helpers/outboundPec";

const props = defineProps<{
  pec: OutboundPECStatus;
}>();

const { t, te } = useI18n();

const drawerOpen = ref(false);
const selectedReceiptId = ref<string | null>(null);

const stateSpec = computed(() => pecOutboundStatusOf(props.pec.state));
const stateLabel = computed(() => {
  const key = pecOutboundLabelKey(props.pec.state);
  return te(key) ? t(key) : props.pec.state;
});

const deliveries = computed(() => props.pec.deliveries ?? []);

const deliveryLabel = (state: string): string => {
  const key = pecDeliveryLabelKey(state);
  return te(key) ? t(key) : state;
};

const formatWhen = (iso?: string): string =>
  iso ? DateUtil.formatDateTime(iso) : EMPTY_VALUE;

const openReceipt = (receiptId: string): void => {
  selectedReceiptId.value = receiptId;
  drawerOpen.value = true;
};
</script>

<template>
  <div class="mb-4">
    <div class="text-subtitle-2 font-weight-bold mb-2">
      {{ t("pec.outbound_block_title") }}
    </div>

    <div class="d-flex align-center flex-wrap ga-2 mb-2">
      <v-chip
        :color="stateSpec.color"
        :prepend-icon="stateSpec.icon"
        label
      >
        {{ stateLabel }}
      </v-chip>
      <span
        v-if="pec.acceptedAt"
        class="text-body-small text-medium-emphasis"
      >
        {{ t("pec.accepted_at") }}: {{ formatWhen(pec.acceptedAt) }}
      </span>
    </div>

    <div
      v-if="!deliveries.length"
      class="text-body-medium text-medium-emphasis"
    >
      {{ t("pec.no_deliveries") }}
    </div>

    <v-timeline
      v-else
      side="end"
      truncate-line="both"
    >
      <v-timeline-item
        v-for="(d, idx) in deliveries"
        :key="`${d.receiptId}-${idx}`"
        :dot-color="pecDeliveryColor(d.state)"
      >
        <div class="text-body-small text-medium-emphasis">
          {{ formatWhen(d.at) }}
        </div>
        <div class="d-flex align-center flex-wrap ga-2 text-body-medium">
          <span class="font-weight-medium">{{ d.recipient }}</span>
          <v-chip
            :color="pecDeliveryColor(d.state)"
            label
          >
            {{ deliveryLabel(d.state) }}
          </v-chip>
        </div>
        <v-btn
          color="primary"
          class="px-0"
          @click="openReceipt(d.receiptId)"
        >
          {{ t("pec.view_receipt") }}
        </v-btn>
      </v-timeline-item>
    </v-timeline>

    <PecReceiptDrawer
      v-model="drawerOpen"
      :receipt-id="selectedReceiptId"
    />
  </div>
</template>
