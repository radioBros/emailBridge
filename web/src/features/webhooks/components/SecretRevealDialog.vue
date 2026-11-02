<script setup lang="ts">
import { ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiAlert, mdiContentCopy, mdiKeyVariant } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import { useNotifyStore } from "@/stores/notify";

const props = defineProps<{
  modelValue: boolean;
  secret: string | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
}>();

const { t } = useI18n();
const notify = useNotifyStore();
const copyAction = actionOf("copy");
const closeAction = actionOf("close");
const copied = ref(false);

watch(
  () => props.modelValue,
  (open) => {
    if (open) copied.value = false;
  },
);

const close = (): void => {
  emit("update:modelValue", false);
};

const onCopy = async (): Promise<void> => {
  if (!props.secret) return;
  try {
    await navigator.clipboard.writeText(props.secret);
    copied.value = true;
    notify.push({ message: t("webhooks.secret_copied"), color: "success" });
  } catch {
    /* ignore */
  }
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="t('webhooks.secret_title')"
    :max-width="520"
    persistent
    @update:model-value="emit('update:modelValue', $event)"
  >
    <template #prepend>
      <v-avatar
        color="warning"
      >
        <v-icon
          :icon="mdiKeyVariant"
        />
      </v-avatar>
    </template>

    <v-alert
      :icon="mdiAlert"
      type="warning"
      class="mb-4"
    >
      {{ t("webhooks.secret_warning") }}
    </v-alert>
    <v-text-field
      :model-value="secret ?? ''"
      :label="t('webhooks.secret_title')"
      class="no-validation font-mono"
      readonly
      hide-details
    >
      <template #append-inner>
        <v-btn
          :color="copyAction.color"
          :prepend-icon="mdiContentCopy"
          :disabled="!secret"
          @click="onCopy"
        >
          {{ t("webhooks.secret_copy") }}
        </v-btn>
      </template>
    </v-text-field>

    <template #footer>
      <v-btn
        :color="closeAction.color"
        @click="close"
      >
        {{ t("webhooks.secret_done") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
