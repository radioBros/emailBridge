<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiOpenInNew } from "@mdi/js";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import StatusChip from "@/components/common/StatusChip.vue";
import { LogLevels } from "@/constants/Statuses";
import type { LogRecord } from "@/api/logs";
import { formatLogTime, prettyJson } from "@/features/logs/helpers/logFormat";
import { actionOf } from "@/constants/Actions";

const props = defineProps<{
  modelValue: boolean;
  record: LogRecord | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
}>();

const { t } = useI18n();
const router = useRouter();
const openAction = actionOf("open");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const levelSpec = computed(() =>
  props.record ? LogLevels[props.record.level] : null,
);

const jumpAccount = (): void => {
  if (!props.record?.accountId) return;
  void router.push({ path: `/accounts/${props.record.accountId}` });
};

const jumpOutbound = (): void => {
  if (!props.record?.outboundId) return;
  void router.push({ path: "/outbox", query: { id: props.record.outboundId } });
};

const jumpDelivery = (): void => {
  if (!props.record?.deliveryId) return;
  void router.push({
    path: "/deliveries",
    query: { id: props.record.deliveryId },
  });
};
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('logs.detail_title')"
    :width="420"
  >
    <template
      v-if="levelSpec && record"
      #prepend
    >
      <StatusChip
        :color="levelSpec.color"
        :icon="levelSpec.icon"
        :label="t(`logs.level.${record.level}`)"
      />
    </template>

    <template v-if="record">
      <div class="text-body-small text-medium-emphasis mb-1">
        {{ formatLogTime(record.time) }}
      </div>
      <div class="text-body-medium mb-4">
        {{ record.msg }}
      </div>

      <div class="d-flex flex-wrap ga-2 mb-4">
        <v-chip
          v-if="record.accountId"
          :prepend-icon="openAction.icon"
          :color="openAction.color"
          label
          @click="jumpAccount"
        >
          {{ t("logs.jump_account") }}
        </v-chip>
        <v-chip
          v-if="record.outboundId"
          :prepend-icon="mdiOpenInNew"
          color="secondary"
          label
          @click="jumpOutbound"
        >
          {{ t("logs.jump_outbound") }}
        </v-chip>
        <v-chip
          v-if="record.deliveryId"
          :prepend-icon="mdiOpenInNew"
          color="secondary"
          label
          @click="jumpDelivery"
        >
          {{ t("logs.jump_delivery") }}
        </v-chip>
      </div>

      <pre class="log-json text-body-small pa-3 rounded-lg">{{ prettyJson(record) }}</pre>
    </template>
  </AppDrawerPanel>
</template>

<style lang="scss" scoped>
.log-json {
  background: rgb(var(--v-theme-surface-variant), 0.35);
  overflow-x: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
