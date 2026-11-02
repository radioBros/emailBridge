<script setup lang="ts">
import { reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import type { SuppressionEntryCreate } from "@/api/suppression";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";

const props = defineProps<{
  modelValue: boolean;
  submitting: boolean;
  errorMsg: string | null;
  fieldErrors: Record<string, string>;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [payload: SuppressionEntryCreate];
}>();

const { t } = useI18n();
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const createAction = actionOf("create");

const form = reactive({
  address: "",
  reason: "",
});

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    form.address = "";
    form.reason = "";
  },
);

const close = (): void => emit("update:modelValue", false);

const onSubmit = async (): Promise<void> => {
  const valid = (await formRef.value?.validate())?.valid ?? true;
  if (!valid) return;
  emit("submit", {
    address: form.address.trim(),
    reason: form.reason.trim() || undefined,
  });
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="t('suppression.entry_form_title')"
    :max-width="480"
    :loading="submitting"
    persistent
    fullscreen-on-mobile
    @update:model-value="emit('update:modelValue', $event)"
  >
    <v-alert
      v-if="errorMsg"
      type="error"
      class="mb-4"
    >
      {{ errorMsg }}
    </v-alert>
    <v-form
      ref="formRef"
      @submit.prevent="onSubmit"
    >
      <v-text-field
        v-model="form.address"
        :label="t('suppression.field_address')"
        :rules="required"
        :error-messages="fieldErrors.address ? [fieldErrors.address] : []"
        class="mb-2"
        type="email"
        data-testid="suppression-entry-address"
      />
      <v-text-field
        v-model="form.reason"
        :label="t('suppression.field_reason')"
        class="no-validation"
        hide-details
      />
    </v-form>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("suppression.cancel") }}
      </v-btn>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        :loading="submitting"
        data-testid="suppression-entry-save"
        @click="onSubmit"
      >
        {{ t("suppression.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
