<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import type { MailboxView } from "@/api/messages";
import AppDialog from "@/components/AppDialog.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";

const props = defineProps<{
  modelValue: boolean;
  mailboxes: MailboxView[];
  excludeId?: string;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  confirm: [mailboxId: string];
}>();

const { t } = useI18n();
const targetId = ref("");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const items = computed(() =>
  props.mailboxes
    .filter((m) => m.id !== props.excludeId)
    .map((m) => ({ title: m.path, value: m.id })),
);

watch(
  () => props.modelValue,
  (v) => {
    if (v) targetId.value = "";
  },
);

const onConfirm = (): void => {
  if (!targetId.value) return;
  emit("confirm", targetId.value);
  open.value = false;
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('messages.move_title')"
    :max-width="480"
  >
    <p class="text-body-medium text-medium-emphasis mb-4">
      {{ t("messages.move_body") }}
    </p>
    <AutocompleteSearch
      v-model="targetId"
      :items="items"
      :label="t('messages.folders')"
      class="no-validation"
      clearable
    />

    <template #footer>
      <v-btn @click="open = false">
        {{ t("messages.back") }}
      </v-btn>
      <v-btn
        :disabled="!targetId"
        color="primary"
        @click="onConfirm"
      >
        {{ t("messages.move_confirm") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
