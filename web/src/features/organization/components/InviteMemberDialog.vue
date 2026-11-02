<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import { email as emailRules, required } from "@/constants/Validations";
import type { OrgRole } from "@/features/organization/helpers/roles";
import AppDialog from "@/components/AppDialog.vue";

const open = defineModel<boolean>("open", { default: false });

const props = defineProps<{
  roles: OrgRole[];
  loading?: boolean;
}>();

const emit = defineEmits<{
  submit: [payload: { email: string; role: string }];
}>();

const { t } = useI18n();
const form = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const email = ref("");
const role = ref<string>("viewer");

const roleItems = computed(() =>
  props.roles.map((r) => ({
    title: t(`organization.roles.${r}`),
    value: r,
  })),
);

watch(open, (v) => {
  if (!v) return;
  email.value = "";
  role.value = props.roles.includes("viewer") ? "viewer" : (props.roles[0] ?? "viewer");
});

const onSubmit = async (): Promise<void> => {
  const res = await form.value?.validate();
  if (!res?.valid) return;
  emit("submit", { email: email.value.trim(), role: role.value });
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organization.members.invite_title')"
    :max-width="480"
    :loading="loading"
  >
    <v-form
      ref="form"
      @submit.prevent="onSubmit"
    >
      <v-text-field
        v-model="email"
        :label="t('organization.members.col_email')"
        :rules="[...required, ...emailRules]"
        type="email"
        autocomplete="email"
        class="mb-2"
      />
      <v-select
        v-model="role"
        :items="roleItems"
        :label="t('organization.members.col_role')"
        :rules="required"
        class="mb-2"
      />
    </v-form>

    <template #footer>
      <v-btn
        @click="open = false"
      >
        {{ t("organization.cancel") }}
      </v-btn>
      <v-btn
        :loading="loading"
        :color="actionOf('invite').color"
        :prepend-icon="actionOf('invite').icon"
        @click="onSubmit"
      >
        {{ t("organization.members.invite") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
