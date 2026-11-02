<script setup lang="ts">
/**
 * Confirmation for a bulk cancel. The body is the consumer's (slot); `typeToConfirm`, when set, asks the
 * user to type that text (the queue name) before the destructive button enables.
 */
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiCancel } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";

const props = withDefaults(
  defineProps<{
    modelValue: boolean;
    title: string;
    confirmLabel: string;
    typeToConfirm?: string;
    loading?: boolean;
  }>(),
  { typeToConfirm: "", loading: false },
);

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  confirm: [typed: string];
}>();

const { t } = useI18n();
const cancelAction = actionOf("cancel");
const typed = ref("");

const open = computed({
  get: (): boolean => props.modelValue,
  set: (v: boolean): void => emit("update:modelValue", v),
});

watch(open, (v) => {
  if (v) typed.value = "";
});

const matches = computed(() => !props.typeToConfirm || typed.value.trim() === props.typeToConfirm);

const rules = [(v: string): true | string => !props.typeToConfirm || v.trim() === props.typeToConfirm || t("queues.type_mismatch")];

const confirm = (): void => {
  if (matches.value) emit("confirm", typed.value.trim());
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="title"
    :loading="loading"
    :max-width="520"
    persistent
  >
    <template #prepend>
      <v-avatar :color="cancelAction.color">
        <v-icon :icon="mdiCancel" />
      </v-avatar>
    </template>

    <div class="d-flex flex-column ga-4">
      <slot />
      <v-text-field
        v-if="typeToConfirm"
        v-model="typed"
        :label="t('queues.type_label', { name: typeToConfirm })"
        :rules="rules"
        :placeholder="typeToConfirm"
        autocomplete="off"
        data-testid="queue-type-confirm"
        spellcheck="false"
        autofocus
        @keydown.enter.prevent="confirm"
      />
    </div>

    <template #footer>
      <v-btn
        :disabled="loading"
        @click="open = false"
      >
        {{ t("queues.keep") }}
      </v-btn>
      <v-btn
        :color="cancelAction.color"
        :prepend-icon="cancelAction.icon"
        :loading="loading"
        :disabled="!matches"
        data-testid="queue-confirm-cancel"
        @click="confirm"
      >
        {{ confirmLabel }}
      </v-btn>
    </template>
  </AppDialog>
</template>
