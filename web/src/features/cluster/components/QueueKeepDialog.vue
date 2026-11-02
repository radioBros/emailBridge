<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import type { QueueView } from "@/api/cluster";

const props = defineProps<{
  modelValue: boolean;
  queue: QueueView | null;
  saving?: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  save: [keep: string];
}>();

const { t } = useI18n();
const keep = ref("");

watch(
  () => [props.modelValue, props.queue] as const,
  ([open, q]) => {
    if (open && q) keep.value = q.keep ?? "";
  },
);

const close = (): void => {
  emit("update:modelValue", false);
};

const onSave = (): void => {
  const v = keep.value.trim();
  if (!v) return;
  emit("save", v);
};
</script>

<template>
  <AppDialog
    :model-value="modelValue && !!queue"
    :title="t('cluster.keep_title')"
    :max-width="420"
    :loading="saving"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template v-if="queue">
      <p class="text-body-medium text-medium-emphasis mb-4">
        {{ t("cluster.keep_body", { name: queue.name }) }}
      </p>
      <v-text-field
        v-model="keep"
        :label="t('cluster.keep_label')"
        class="no-validation"
        hide-details="auto"
        @keyup.enter="onSave"
      />
    </template>

    <template #footer>
      <v-btn @click="close">
        {{ t("cluster.keep_cancel") }}
      </v-btn>
      <v-btn
        :color="actionOf('save').color"
        :loading="saving"
        :disabled="!keep.trim()"
        @click="onSave"
      >
        {{ t("cluster.keep_save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
