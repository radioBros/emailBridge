<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiDelete } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import type { Job } from "@/api/organizations";
import type { OrganizationRow } from "@/features/organizations/composables/useOrganizations";

const open = defineModel<boolean>("open", { default: false });

const props = withDefaults(
  defineProps<{
    organization: OrganizationRow | null;
    loading?: boolean;
    job?: Job | null;
    polling?: boolean;
    progress?: string;
  }>(),
  { loading: false, job: null, polling: false, progress: "" },
);

const emit = defineEmits<{
  confirm: [];
}>();

const { t } = useI18n();
const deleteAction = actionOf("delete");
const typed = ref("");

const typeToConfirm = computed(() => props.organization?.slug ?? "");

watch(open, (v) => {
  if (v) typed.value = "";
});

const matches = computed(
  () => !typeToConfirm.value || typed.value.trim() === typeToConfirm.value,
);

const rules = [
  (v: string): true | string =>
    !typeToConfirm.value || v.trim() === typeToConfirm.value || t("organizations.type_mismatch"),
];

const showProgress = computed(() => props.polling || Boolean(props.job));

const onConfirm = (): void => {
  if (!matches.value || props.loading || props.polling) return;
  emit("confirm");
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organizations.delete_title')"
    :max-width="520"
    :loading="loading && !polling"
    persistent
  >
    <template #prepend>
      <v-avatar :color="deleteAction.color">
        <v-icon :icon="mdiDelete" />
      </v-avatar>
    </template>

    <div class="d-flex flex-column ga-4">
      <p class="text-body-medium mb-0">
        {{ t("organizations.delete_body", { name: organization?.name ?? "" }) }}
      </p>

      <v-text-field
        v-if="!showProgress"
        v-model="typed"
        :label="t('organizations.type_label', { slug: typeToConfirm })"
        :rules="rules"
        :placeholder="typeToConfirm"
        autocomplete="off"
        spellcheck="false"
        autofocus
        @keydown.enter.prevent="onConfirm"
      />

      <div
        v-else
        class="d-flex flex-column ga-2"
      >
        <v-progress-linear
          color="error"
          indeterminate
        />
        <p class="text-body-small text-medium-emphasis mb-0">
          {{ t("organizations.delete_progress", { state: progress || job?.state }) }}
        </p>
      </div>
    </div>

    <template #footer>
      <v-btn
        :disabled="loading || polling"
        @click="open = false"
      >
        {{ t("organizations.cancel") }}
      </v-btn>
      <v-btn
        v-if="!showProgress"
        :color="deleteAction.color"
        :prepend-icon="deleteAction.icon"
        :loading="loading"
        :disabled="!matches"
        @click="onConfirm"
      >
        {{ t("organizations.delete") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
