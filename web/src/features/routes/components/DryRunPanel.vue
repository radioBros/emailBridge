<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiPlayCircle } from "@mdi/js";
import type { Job, RouteDryRunResult } from "@/api/routes";
import { actionOf } from "@/constants/Actions";

defineProps<{
  job: Job | null;
  result: RouteDryRunResult | null;
  error: string | null;
  loading: boolean;
}>();

const emit = defineEmits<{
  run: [opts: { windowHours: number; limit: number }];
}>();

const { t } = useI18n();
const runAction = actionOf("test");
const windowHours = ref(24);
const limit = ref(1000);

const pctOf = (job: Job | null): number => {
  if (!job?.progress?.total) return job?.state === "succeeded" ? 100 : 0;
  return Math.min(100, Math.round((100 * (job.progress.done || 0)) / job.progress.total));
};

const samplesText = (result: RouteDryRunResult): string => {
  try {
    return JSON.stringify(result.samples, null, 2);
  } catch {
    return "";
  }
};
</script>

<template>
  <div>
    <div class="d-flex align-center justify-space-between flex-wrap ga-2 mb-3">
      <div class="d-flex align-center ga-2">
        <v-avatar-small
          :color="runAction.color"
        >
          <v-icon
            :icon="mdiPlayCircle"
          />
        </v-avatar-small>
        <span class="text-subtitle-2">
          {{ t("routes.dryrun_title") }}
        </span>
      </div>
      <v-btn
        :color="runAction.color"
        :prepend-icon="runAction.icon"
        :loading="loading"
        @click="emit('run', { windowHours: windowHours, limit: limit })"
      >
        {{ t("routes.dryrun_run") }}
      </v-btn>
    </div>

    <v-row>
      <v-col
        cols="6"
        md="4"
      >
        <v-text-field
          v-model.number="windowHours"
          :label="t('routes.dryrun_window')"
          class="no-validation"
          type="number"
          min="1"
          max="24"
          hide-details
        />
      </v-col>
      <v-col
        cols="6"
        md="4"
      >
        <v-text-field
          v-model.number="limit"
          :label="t('routes.dryrun_limit')"
          class="no-validation"
          type="number"
          min="1"
          max="1000"
          hide-details
        />
      </v-col>
    </v-row>

    <v-alert
      v-if="error"
      type="error"
      class="mt-3"
    >
      {{ error }}
    </v-alert>

    <v-sheet
      v-if="job"
      rounded="lg"
      class="pa-3 mt-3"
      border
    >
      <div class="d-flex align-center ga-2 mb-2">
        <v-chip
          :color="job.state === 'succeeded' ? 'success' : job.state === 'failed' ? 'error' : 'info'"
        >
          {{ t(`routes.job_state_${job.state}`) }}
        </v-chip>
        <span class="text-caption text-medium-emphasis">
          {{ job.id }}
        </span>
      </div>
      <v-progress-linear
        :model-value="pctOf(job)"
        :indeterminate="job.state === 'queued' || (job.state === 'running' && !job.progress)"
        color="accent"
        height="6"
        class="mb-2"
        rounded
      />
      <div
        v-if="job.progress"
        class="text-caption text-medium-emphasis"
      >
        {{ job.progress.done }} / {{ job.progress.total }}
      </div>
    </v-sheet>

    <v-sheet
      v-if="result"
      rounded="lg"
      class="pa-3 mt-3"
      border
    >
      <div class="text-body-2 mb-1">
        <span class="text-medium-emphasis">{{ t("routes.dryrun_matched") }}:</span>
        {{ result.matched }} / {{ result.scanned }}
      </div>
      <div class="text-body-2 mb-1">
        <span class="text-medium-emphasis">{{ t("routes.dryrun_cost") }}:</span>
        {{ result.cost.total }} (max {{ result.cost.max }}, avg {{ result.cost.avg.toFixed(1) }})
      </div>
      <div
        v-if="Object.keys(result.errorsByPhase || {}).length"
        class="text-body-2 mb-2"
      >
        <span class="text-medium-emphasis">{{ t("routes.dryrun_errors") }}:</span>
        {{ JSON.stringify(result.errorsByPhase) }}
      </div>
      <div class="text-medium-emphasis text-body-2 mb-1">
        {{ t("routes.dryrun_samples") }}
      </div>
      <pre class="text-caption font-mono mb-0 text-wrap">{{ samplesText(result) }}</pre>
    </v-sheet>
  </div>
</template>
