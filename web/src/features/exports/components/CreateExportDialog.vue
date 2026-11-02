<script setup lang="ts">
import { computed, reactive, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { ExportCreateInput } from "@/api/exports";
import type { MailboxView } from "@/api/messages";
import { actionOf } from "@/constants/Actions";

const props = defineProps<{
  modelValue: boolean;
  mailboxes: MailboxView[];
  submitting: boolean;
  errorMsg: string | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  submit: [payload: ExportCreateInput];
}>();

const { t } = useI18n();
const createAction = actionOf("create");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const form = reactive({
  folderIds: [] as string[],
  since: "",
  before: "",
  metadataOnly: true,
  raw: false,
  attachments: false,
});

watch(
  () => props.modelValue,
  (v) => {
    if (!v) return;
    form.folderIds = [];
    form.since = "";
    form.before = "";
    form.metadataOnly = true;
    form.raw = false;
    form.attachments = false;
  },
);

watch(
  () => [form.raw, form.attachments] as const,
  ([raw, att]) => {
    if (raw || att) form.metadataOnly = false;
  },
);

watch(
  () => form.metadataOnly,
  (meta) => {
    if (meta) {
      form.raw = false;
      form.attachments = false;
    }
  },
);

const folderItems = computed(() =>
  props.mailboxes.map((m) => ({ title: m.path, value: m.id })),
);

const onSubmit = (): void => {
  const body: ExportCreateInput = {
    include: form.metadataOnly
      ? { metadataOnly: true }
      : { raw: form.raw || undefined, attachments: form.attachments || undefined },
  };
  if (form.folderIds.length) body.folderIds = [...form.folderIds];
  if (form.since) body.since = new Date(form.since).toISOString();
  if (form.before) body.before = new Date(form.before).toISOString();
  emit("submit", body);
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('exports.create_title')"
    :max-width="560"
    :loading="submitting"
    fullscreen-on-mobile
  >
    <v-alert
      v-if="errorMsg"
      type="error"
      class="mb-4"
    >
      {{ errorMsg }}
    </v-alert>
    <AutocompleteSearch
      v-model="form.folderIds"
      :items="folderItems"
      :label="t('exports.folders')"
      :hint="t('exports.folders_hint')"
      class="no-validation mb-2"
      multiple
      chips
      clearable
      persistent-hint
    />
    <v-row>
      <v-col
        cols="12"
        md="6"
      >
        <v-text-field
          v-model="form.since"
          :label="t('exports.since')"
          class="no-validation"
          type="datetime-local"
          clearable
        />
      </v-col>
      <v-col
        cols="12"
        md="6"
      >
        <v-text-field
          v-model="form.before"
          :label="t('exports.before')"
          class="no-validation"
          type="datetime-local"
          clearable
        />
      </v-col>
    </v-row>
    <v-checkbox
      v-model="form.metadataOnly"
      :label="t('exports.include_metadata')"
      class="no-validation"
      hide-details
    />
    <v-checkbox
      v-model="form.raw"
      :label="t('exports.include_raw')"
      class="no-validation"
      hide-details
    />
    <v-checkbox
      v-model="form.attachments"
      :label="t('exports.include_attachments')"
      class="no-validation"
      hide-details
    />

    <template #footer>
      <v-btn @click="open = false">
        {{ t("exports.cancel") }}
      </v-btn>
      <v-btn
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        :loading="submitting"
        @click="onSubmit"
      >
        {{ t("exports.create_submit") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
