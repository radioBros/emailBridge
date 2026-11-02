<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import DateRangeField from "@/features/events/components/DateRangeField.vue";
import { actionOf } from "@/constants/Actions";
import { listWebhookEndpoints } from "@/api/webhooks";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { EVENT_TYPES } from "@/features/events/helpers/types";
import { jobProgressText, startEventsReplay } from "@/features/events/helpers/replayEvents";
import AppDialog from "@/components/AppDialog.vue";

const props = defineProps<{
  modelValue: boolean;
  initialFrom?: string;
  initialTo?: string;
  initialType?: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
}>();

const { t } = useI18n();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const replayAction = actionOf("replay");

const from = ref("");
const to = ref("");
const endpointId = ref("");
const typeFilter = ref("");
const ignoreEndpointFilter = ref(false);
const loading = ref(false);
const jobBusy = ref(false);
const jobProgress = ref("");
const endpoints = ref<{ title: string; value: string }[]>([]);
const endpointsLoading = ref(false);

const typeItems = computed(() => EVENT_TYPES.map((e) => ({ title: e, value: e })));
const canSubmit = computed(() => !!endpointId.value && !!from.value && !loading.value);

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    from.value = props.initialFrom || "";
    to.value = props.initialTo || "";
    typeFilter.value = props.initialType || "";
    endpointId.value = "";
    ignoreEndpointFilter.value = false;
    jobProgress.value = "";
    void loadEndpoints("");
  },
  { immediate: true },
);

const loadEndpoints = async (q: string): Promise<void> => {
  endpointsLoading.value = true;
  try {
    const res = await listWebhookEndpoints({ page: 1, limit: 50, q: q || undefined });
    endpoints.value = (res.data ?? []).map((e) => ({
      title: e.name,
      value: e.id,
    }));
  } catch (err) {
    endpoints.value = [];
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    endpointsLoading.value = false;
  }
};

const close = (): void => emit("update:modelValue", false);

const confirm = async (): Promise<void> => {
  if (!canSubmit.value) return;
  loading.value = true;
  jobBusy.value = true;
  try {
    const body: {
      endpointId: string;
      from: string;
      to?: string;
      types?: string[];
      ignoreEndpointFilter?: boolean;
    } = {
      endpointId: endpointId.value,
      from: from.value,
    };
    if (to.value) body.to = to.value;
    if (typeFilter.value) body.types = [typeFilter.value];
    if (ignoreEndpointFilter.value) body.ignoreEndpointFilter = true;
    close();
    const job = await startEventsReplay(body, (j) => {
      jobProgress.value = jobProgressText(j);
      notify.push({
        message: t("events.replay_job_progress", { progress: jobProgress.value }),
        color: "info",
      });
    });
    const color =
      job.state === "succeeded" ? "success" : job.state === "cancelled" ? "warning" : "error";
    notify.push({ message: t(`events.replay_job_${job.state}`), color });
  } catch (err) {
    notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
  } finally {
    loading.value = false;
    jobBusy.value = false;
  }
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="t('events.replay_title')"
    :max-width="520"
    :loading="loading || jobBusy"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <p class="text-body-medium mb-4">
      {{ t("events.replay_body") }}
    </p>
    <DateRangeField
      v-model:from="from"
      v-model:to="to"
      :label="t('events.replay_dates')"
      class="mb-3"
    />
    <AutocompleteSearch
      v-model="endpointId"
      :items="endpoints"
      :loading="endpointsLoading"
      :label="t('events.replay_endpoint')"
      class="no-validation mb-3"
      server-search
      clearable
      hide-details
      @update:search="loadEndpoints"
    />
    <AutocompleteSearch
      v-model="typeFilter"
      :items="typeItems"
      :label="t('events.replay_types')"
      class="no-validation mb-3"
      clearable
      hide-details
    />
    <v-switch
      v-model="ignoreEndpointFilter"
      :label="t('events.replay_ignore_filter')"
      color="primary"
      class="no-validation"
      hide-details
    />
    <p class="text-caption text-medium-emphasis mt-2 mb-0">
      {{ t("events.replay_ignore_hint") }}
    </p>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("common.cancel") }}
      </v-btn>
      <v-btn
        :color="replayAction.color"
        :loading="loading || jobBusy"
        :disabled="!canSubmit"
        @click="confirm"
      >
        {{ t("events.replay_confirm") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
