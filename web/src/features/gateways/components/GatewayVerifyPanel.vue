<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { mdiCheckCircle, mdiCloseCircle } from "@mdi/js";
import type { GatewayVerifyResult, GatewayView } from "@/api/gateways";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";

defineProps<{
  modelValue: boolean;
  gateway: GatewayView | null;
  result: GatewayVerifyResult | null;
  errorMsg: string | null;
  loading: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  run: [];
}>();

const { t } = useI18n();
const verifyAction = actionOf("verify");
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :max-width="520"
    :loading="loading"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #title>
      {{ t("gateways.verify_title") }}
      <span
        v-if="gateway"
        class="text-medium-emphasis"
      >
        — {{ gateway.name }}
      </span>
    </template>

    <div class="position-relative">
      <AppSectionLoader :status="loading" />
      <p class="text-body-medium text-medium-emphasis mb-4">
        {{ t("gateways.verify_hint") }}
      </p>
      <v-alert
        v-if="errorMsg"
        type="error"
        class="mb-4"
      >
        {{ errorMsg }}
      </v-alert>
      <div
        v-if="result"
        class="d-flex align-start ga-3"
      >
        <v-icon-status
          :icon="result.ok ? mdiCheckCircle : mdiCloseCircle"
          :color="result.ok ? 'success' : 'error'"
        />
        <div class="min-w-0">
          <div class="text-title-medium font-weight-bold">
            {{
              result.ok
                ? t("gateways.verify_ok", { ms: result.latencyMs })
                : t("gateways.verify_fail")
            }}
          </div>
          <div
            v-if="result.code || result.detail"
            class="text-body-small text-medium-emphasis mt-1"
          >
            <span
              v-if="result.code"
              class="font-mono me-2"
            >{{ result.code }}</span>
            {{ result.detail }}
          </div>
          <div
            v-if="result.capabilities?.length"
            class="mt-3"
          >
            <div class="text-caption text-medium-emphasis mb-1">
              {{ t("gateways.verify_caps") }}
            </div>
            <div class="d-flex flex-wrap ga-1">
              <v-chip
                v-for="c in result.capabilities"
                :key="c"
              >
                {{ c }}
              </v-chip>
            </div>
          </div>
        </div>
      </div>
    </div>

    <template #footer>
      <v-btn @click="emit('update:modelValue', false)">
        {{ t("gateways.cancel") }}
      </v-btn>
      <v-btn
        :color="verifyAction.color"
        :prepend-icon="verifyAction.icon"
        :loading="loading"
        data-testid="gateway-verify-run"
        @click="emit('run')"
      >
        {{ t("gateways.verify_run") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
