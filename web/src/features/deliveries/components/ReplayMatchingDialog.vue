<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import AppDialog from "@/components/AppDialog.vue";

const props = defineProps<{
  modelValue: boolean;
  total: number;
  loading?: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  confirm: [includeReplayed: boolean];
}>();

const { t } = useI18n();
const replayAction = actionOf("replay");
const includeReplayed = ref(false);

watch(
  () => props.modelValue,
  (open) => {
    if (open) includeReplayed.value = false;
  },
);

const close = (): void => emit("update:modelValue", false);
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="t('deliveries.replay_matching_title')"
    :max-width="480"
    :loading="loading"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <p class="text-body-medium mb-4">
      {{ t("deliveries.replay_matching_body", { total }) }}
    </p>
    <v-switch
      v-model="includeReplayed"
      :label="t('deliveries.include_replayed')"
      color="primary"
      class="no-validation"
      hide-details
    />
    <p class="text-caption text-medium-emphasis mt-2 mb-0">
      {{ t("deliveries.include_replayed_hint") }}
    </p>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("common.cancel") }}
      </v-btn>
      <v-btn
        :color="replayAction.color"
        :loading="loading"
        @click="emit('confirm', includeReplayed)"
      >
        {{ t("deliveries.replay_matching") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
