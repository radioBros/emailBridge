<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import type {
  SuppressionListCreate,
  SuppressionListUpdate,
  SuppressionListView,
} from "@/api/suppression";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import { SUPPRESSION_KINDS } from "@/features/suppression/helpers/kinds";

const props = defineProps<{
  modelValue: boolean;
  list: SuppressionListView | null;
  submitting: boolean;
  errorMsg: string | null;
  fieldErrors: Record<string, string>;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [payload: SuppressionListCreate | (SuppressionListUpdate & { id: string })];
}>();

const { t } = useI18n();
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const saveAction = actionOf("save");
const createAction = actionOf("create");

const form = reactive({
  name: "",
  kind: "manual",
  description: "",
});

const isEdit = computed(() => Boolean(props.list));
const title = computed(() =>
  isEdit.value ? t("suppression.form_title_edit") : t("suppression.form_title_create"),
);

const kindItems = computed(() =>
  SUPPRESSION_KINDS.map((k) => ({ title: t(`suppression.kind_${k}`), value: k })),
);

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    form.name = props.list?.name ?? "";
    form.kind = props.list?.kind || "manual";
    form.description = props.list?.description ?? "";
  },
);

const close = (): void => emit("update:modelValue", false);

const onSubmit = async (): Promise<void> => {
  const valid = (await formRef.value?.validate())?.valid ?? true;
  if (!valid) return;
  if (isEdit.value && props.list) {
    emit("submit", {
      id: props.list.id,
      name: form.name.trim(),
      description: form.description.trim() || undefined,
    });
    return;
  }
  emit("submit", {
    name: form.name.trim(),
    kind: form.kind,
    description: form.description.trim() || undefined,
  });
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="title"
    :max-width="520"
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
        v-model="form.name"
        :label="t('suppression.field_name')"
        :rules="required"
        :error-messages="fieldErrors.name ? [fieldErrors.name] : []"
        class="mb-2"
        data-testid="suppression-list-name"
      />
      <v-select
        v-if="!isEdit"
        v-model="form.kind"
        :items="kindItems"
        :label="t('suppression.field_kind')"
        class="no-validation mb-2"
        hide-details
      />
      <v-textarea
        v-model="form.description"
        :label="t('suppression.field_description')"
        class="no-validation"
        rows="3"
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
        :color="isEdit ? saveAction.color : createAction.color"
        :prepend-icon="isEdit ? saveAction.icon : createAction.icon"
        :loading="submitting"
        data-testid="suppression-list-save"
        @click="onSubmit"
      >
        {{ t("suppression.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
