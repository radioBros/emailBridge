<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiFlask } from "@mdi/js";
import type { RouteTestResult } from "@/api/routes";
import { actionOf } from "@/constants/Actions";
import { parseCelErrorPosition } from "@/features/routes/celHighlight";

const props = defineProps<{
  result: RouteTestResult | null;
  error: string | null;
  loading: boolean;
}>();

const emit = defineEmits<{
  run: [eventJson: string];
}>();

const { t } = useI18n();
const testAction = actionOf("test");
const eventJson = ref(`{
  "specversion": "1.0",
  "type": "message.received",
  "id": "evt_demo",
  "source": "/organizations/org_demo",
  "data": {}
}`);

const position = computed(() => parseCelErrorPosition(props.result?.error ?? props.error));

const mappedText = computed(() => {
  if (!props.result || props.result.mapped === undefined) return "";
  try {
    return JSON.stringify(props.result.mapped, null, 2);
  } catch {
    return String(props.result.mapped);
  }
});
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
          {{ t("routes.test_title") }}
        </span>
      </div>
      <v-btn
        :color="testAction.color"
        :prepend-icon="testAction.icon"
        :loading="loading"
        @click="emit('run', eventJson)"
      >
        {{ t("routes.test_run") }}
      </v-btn>
    </div>

    <v-textarea
      v-model="eventJson"
      :label="t('routes.test_event')"
      :hint="t('routes.test_event_hint')"
      class="no-validation font-mono"
      rows="8"
      spellcheck="false"
      persistent-hint
    />

    <v-alert
      v-if="error"
      type="error"
      class="mt-3"
    >
      {{ error }}
      <div
        v-if="position.line != null"
        class="text-caption mt-1"
      >
        {{ t("routes.test_pos", { line: position.line, column: position.column }) }}
      </div>
    </v-alert>

    <v-sheet
      v-if="result"
      rounded="lg"
      class="pa-3 mt-3"
      border
    >
      <v-chip
        :color="result.matched ? 'success' : 'warning'"
        class="mb-3"
      >
        {{ result.matched ? t("routes.test_matched") : t("routes.test_not_matched") }}
      </v-chip>
      <div class="text-body-2 mb-1">
        <span class="text-medium-emphasis">{{ t("routes.test_cost") }}:</span>
        {{ result.costUsed }}
      </div>
      <div class="text-body-2 mb-2">
        <span class="text-medium-emphasis">{{ t("routes.test_duration") }}:</span>
        {{ result.durationMs }} ms
      </div>
      <div
        v-if="result.error"
        class="text-body-2 text-error mb-2"
      >
        <div class="text-medium-emphasis">
          {{ t("routes.test_error") }}
        </div>
        {{ result.error }}
        <div
          v-if="position.line != null"
          class="text-caption mt-1"
        >
          {{ t("routes.test_pos", { line: position.line, column: position.column }) }}
        </div>
      </div>
      <div v-if="mappedText">
        <div class="text-medium-emphasis text-body-2 mb-1">
          {{ t("routes.test_mapped") }}
        </div>
        <pre class="text-caption font-mono mb-0 text-wrap">{{ mappedText }}</pre>
      </div>
    </v-sheet>
  </div>
</template>
