<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiDomainPlus } from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import { email as emailRules, required } from "@/constants/Validations";
import { isValidOrgSlug, slugifyOrgName } from "@/features/organizations/helpers/slug";

const open = defineModel<boolean>("open", { default: false });

defineProps<{
  loading?: boolean;
  slugError?: string | null;
}>();

const emit = defineEmits<{
  submit: [payload: { name: string; slug: string; ownerEmail: string }];
  "clear-slug-error": [];
}>();

const { t } = useI18n();
const createAction = actionOf("create");
const form = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const name = ref("");
const slug = ref("");
const slugTouched = ref(false);
const ownerEmail = ref("");

watch(name, (v) => {
  if (slugTouched.value) return;
  slug.value = slugifyOrgName(v);
});

watch(open, (v) => {
  if (!v) return;
  name.value = "";
  slug.value = "";
  slugTouched.value = false;
  ownerEmail.value = "";
  emit("clear-slug-error");
});

const slugRules = computed(() => [
  ...required,
  (v: string): true | string =>
    isValidOrgSlug(String(v).trim()) || t("organizations.slug_invalid"),
]);

const onSlugInput = (): void => {
  slugTouched.value = true;
  slug.value = slug.value.toLowerCase().replace(/[^a-z0-9-]/g, "");
  emit("clear-slug-error");
};

const title = computed(() => t("organizations.create_title"));

const ownerEmailRules = computed(() =>
  ownerEmail.value.trim() ? emailRules : [],
);

const onSubmit = async (): Promise<void> => {
  const res = await form.value?.validate();
  if (!res?.valid) return;
  emit("submit", {
    name: name.value.trim(),
    slug: slug.value.trim(),
    ownerEmail: ownerEmail.value.trim(),
  });
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="title"
    :max-width="520"
    :loading="loading"
  >
    <template #prepend>
      <v-avatar :color="createAction.color">
        <v-icon :icon="mdiDomainPlus" />
      </v-avatar>
    </template>

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
        v-model="slug"
        :label="t('organizations.field_slug')"
        :rules="slugRules"
        :hint="t('organizations.field_slug_hint')"
        :error-messages="slugError ? [slugError] : []"
        class="mb-2"
        persistent-hint
        @update:model-value="onSlugInput"
      />
      <v-text-field
        v-model="ownerEmail"
        :label="t('organizations.field_owner_email')"
        :hint="t('organizations.field_owner_email_hint')"
        :rules="ownerEmailRules"
        :class="ownerEmail.trim() ? 'mb-2' : 'no-validation mb-2'"
        type="email"
        autocomplete="email"
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
        :color="createAction.color"
        :prepend-icon="createAction.icon"
        :loading="loading"
        @click="onSubmit"
      >
        {{ t("organizations.create") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
