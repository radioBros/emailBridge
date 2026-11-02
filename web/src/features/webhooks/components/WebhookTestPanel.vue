<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { mdiFlask } from "@mdi/js";
import type { DeliveryAttempt } from "@/api/webhooks";
import { actionOf } from "@/constants/Actions";
import { EMPTY_VALUE } from "@/helpers/Format";

defineProps<{
  result: DeliveryAttempt | null;
  error: string | null;
  loading: boolean;
}>();

const emit = defineEmits<{
  run: [];
}>();

const { t } = useI18n();
const testAction = actionOf("test");
</script>

<template>
  <div>
    <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-3">
      <div class="d-flex align-center ga-2">
        <v-avatar-small
          :color="testAction.color"
        >
          <v-icon
            :icon="mdiFlask"
          />
        </v-avatar-small>
        <span class="text-subtitle-2">
          {{ t("webhooks.test_title") }}
        </span>
      </div>
      <v-btn
        :color="testAction.color"
        :prepend-icon="testAction.icon"
        :loading="loading"
        @click="emit('run')"
      >
        {{ t("webhooks.test_run") }}
      </v-btn>
    </div>

    <v-alert
      v-if="error"
      type="error"
      class="mb-3"
    >
      {{ error }}
    </v-alert>

    <v-sheet
      v-if="result"
      rounded="lg"
      class="pa-3"
      border
    >
      <div class="text-body-2 mb-1">
        <span class="text-medium-emphasis">{{ t("webhooks.test_status") }}:</span>
        {{ result.statusCode ?? EMPTY_VALUE }}
        <span
          v-if="result.kind"
          class="ms-2"
        >{{ result.kind }}</span>
      </div>
      <div class="text-body-2 mb-1">
        <span class="text-medium-emphasis">{{ t("webhooks.test_latency") }}:</span>
        {{ result.latencyMs }} ms
      </div>
      <div
        v-if="result.error"
        class="text-body-2 text-error mb-1"
      >
        <span class="text-medium-emphasis">{{ t("webhooks.test_error") }}:</span>
        {{ result.error }}
      </div>
      <div
        v-if="result.responseExcerpt"
        class="text-body-2"
      >
        <div class="text-medium-emphasis mb-1">
          {{ t("webhooks.test_excerpt") }}
        </div>
        <pre class="text-caption font-mono mb-0 text-wrap">{{ result.responseExcerpt }}</pre>
      </div>
    </v-sheet>
  </div>
</template>
