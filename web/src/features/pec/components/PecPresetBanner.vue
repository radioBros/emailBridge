<script setup lang="ts">
import { computed, toRef } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCheck, mdiCertificate } from "@mdi/js";
import type { PECDefaults } from "@/api/pec";
import { actionOf } from "@/constants/Actions";
import { usePecDiscovery } from "@/features/pec/composables/usePecDiscovery";

const props = defineProps<{
  email: string;
}>();

const emit = defineEmits<{
  "apply-enabled": [];
  "apply-inbox": [defaults: PECDefaults | null];
  "apply-policy": [defaults: PECDefaults | null];
}>();

const { t } = useI18n();
const applyAction = actionOf("apply");

const { pecDefaults, loading, applied, showBanner, markApplied } = usePecDiscovery(
  toRef(props, "email"),
);

const syncHint = computed(() => {
  const paths = pecDefaults.value?.syncPaths;
  if (paths?.length) return paths.join(", ");
  return "INBOX";
});

const policyHint = computed(() => pecDefaults.value?.sendPolicy || "atMostOnce");

const onEnable = (): void => {
  emit("apply-enabled");
  markApplied("enabled");
};

const onInbox = (): void => {
  emit("apply-inbox", pecDefaults.value);
  markApplied("inbox");
};

const onPolicy = (): void => {
  emit("apply-policy", pecDefaults.value);
  markApplied("policy");
};
</script>

<template>
  <v-alert
    v-if="showBanner"
    type="info"
    class="mb-4"
    data-testid="pec-preset-banner"
  >
    <div class="d-flex align-start ga-3">
      <v-icon
        :icon="mdiCertificate"
        color="info"
        class="mt-1 flex-shrink-0"
      />
      <div class="flex-grow-1 min-w-0">
        <div class="text-body-medium font-weight-bold mb-1">
          {{ t("pec.preset_title") }}
        </div>
        <div class="text-body-small mb-3">
          {{ t("pec.preset_body") }}
        </div>
        <div class="d-flex flex-wrap ga-2">
          <v-btn
            :color="applied.enabled ? 'success' : applyAction.color"
            :prepend-icon="applied.enabled ? mdiCheck : undefined"
            :loading="loading"
            data-testid="pec-apply-enabled"
            @click="onEnable"
          >
            {{ t("pec.preset_enable") }}
          </v-btn>
          <v-btn
            :color="applied.inbox ? 'success' : applyAction.color"
            :prepend-icon="applied.inbox ? mdiCheck : undefined"
            data-testid="pec-apply-inbox"
            @click="onInbox"
          >
            {{ t("pec.preset_inbox", { paths: syncHint }) }}
          </v-btn>
          <v-btn
            :color="applied.policy ? 'success' : applyAction.color"
            :prepend-icon="applied.policy ? mdiCheck : undefined"
            data-testid="pec-apply-policy"
            @click="onPolicy"
          >
            {{ t("pec.preset_policy", { policy: policyHint }) }}
          </v-btn>
        </div>
      </div>
    </div>
  </v-alert>
</template>
