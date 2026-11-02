<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import type { OrganizationRow } from "@/features/organizations/composables/useOrganizations";

const open = defineModel<boolean>("open", { default: false });

const props = defineProps<{
  organization: OrganizationRow | null;
  loading?: boolean;
}>();

const emit = defineEmits<{
  submit: [payload: { name: string; status: "active" | "suspended" }];
}>();

const { t } = useI18n();
const saveAction = actionOf("save");
const form = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const name = ref("");
const status = ref<"active" | "suspended">("active");

const statusItems = computed(() => [
  { title: t("organizations.status_active"), value: "active" },
  { title: t("organizations.status_suspended"), value: "suspended" },
]);

watch(open, (v) => {
  if (!v) return;
  name.value = props.organization?.name ?? "";
  status.value =
    props.organization?.status === "suspended" ? "suspended" : "active";
});

const onSubmit = async (): Promise<void> => {
  const res = await form.value?.validate();
  if (!res?.valid) return;
  emit("submit", { name: name.value.trim(), status: status.value });
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organizations.edit_title')"
    :max-width="480"
    :loading="loading"
  >
    <v-form
      ref="form"
      @submit.prevent="onSubmit"
    >
      <v-text-field
        v-model="name"
        :label="t('organizations.field_name')"
        :rules="required"
        autocomplete="organization"
        class="mb-2"
      />
      <v-text-field
        v-if="organization"
        :model-value="organization.slug"
        :label="t('organizations.field_slug')"
        class="no-validation mb-2"
        readonly
      />
      <v-select
        v-model="status"
        :items="statusItems"
        :label="t('organizations.field_status')"
        :hint="t('organizations.field_status_hint')"
        class="mb-2 no-validation"
        persistent-hint
      />
    </v-form>

    <template #footer>
      <v-btn
        @click="open = false"
      >
        {{ t("organizations.cancel") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="loading"
        @click="onSubmit"
      >
        {{ t("organizations.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
