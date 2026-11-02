<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import type { OrgRole } from "@/features/organization/helpers/roles";
import type { MemberRow } from "@/features/organization/composables/useMembers";
import AppDialog from "@/components/AppDialog.vue";

const open = defineModel<boolean>("open", { default: false });

const props = defineProps<{
  member: MemberRow | null;
  roles: OrgRole[];
  loading?: boolean;
}>();

const emit = defineEmits<{
  submit: [role: string];
}>();

const { t } = useI18n();
const role = ref("viewer");

const roleItems = computed(() =>
  props.roles.map((r) => ({
    title: t(`organization.roles.${r}`),
    value: r,
  })),
);

watch(
  () => [open.value, props.member] as const,
  ([isOpen, m]) => {
    if (isOpen && m) role.value = m.role;
  },
);

const onSubmit = (): void => {
  emit("submit", role.value);
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organization.members.edit_role_title')"
    :max-width="420"
    :loading="loading"
  >
    <p class="text-body-medium mb-4">
      {{ member?.email }}
    </p>
    <v-select
      v-model="role"
      :items="roleItems"
      :label="t('organization.members.col_role')"
      :rules="required"
    />

    <template #footer>
      <v-btn
        @click="open = false"
      >
        {{ t("organization.cancel") }}
      </v-btn>
      <v-btn
        :loading="loading"
        :color="actionOf('save').color"
        :prepend-icon="actionOf('save').icon"
        @click="onSubmit"
      >
        {{ t("organization.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
