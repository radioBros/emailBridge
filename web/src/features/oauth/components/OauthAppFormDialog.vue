<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { required } from "@/constants/Validations";
import type { OauthApp, OauthAppCreate, OauthAppPatch, OauthProvider } from "@/api/oauth";
import AppDialog from "@/components/AppDialog.vue";


const props = defineProps<{
  modelValue: boolean;
  app?: OauthApp | null;
  saving?: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [boolean];
  save: [OauthAppCreate | OauthAppPatch];
}>();

const { t } = useI18n();
const form = ref<HTMLFormElement | null>(null);
const name = ref("");
const provider = ref<OauthProvider>("gmail");
const clientId = ref("");
const clientSecret = ref("");
const serviceKey = ref("");
const tenantId = ref("");
const appOnly = ref(false);
const authorizeUrl = ref("");
const tokenUrl = ref("");
const revokeUrl = ref("");
const scopes = ref("");
const enabled = ref(true);

const isEdit = computed(() => Boolean(props.app));
const title = computed(() =>
  isEdit.value ? t("oauth.form_title_edit") : t("oauth.form_title_create"),
);

const providers = computed(() =>
  (["gmail", "outlook", "mailru", "generic"] as OauthProvider[]).map((p) => ({
    title: t(`oauth.provider_${p}`),
    value: p,
  })),
);

const showGenericUrls = computed(() => provider.value === "generic");
const showGmailSA = computed(() => provider.value === "gmail");
const showOutlookTenant = computed(() => provider.value === "outlook");

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    const a = props.app;
    name.value = a?.name ?? "";
    provider.value = a?.provider ?? "gmail";
    clientId.value = a?.clientId ?? "";
    clientSecret.value = "";
    serviceKey.value = "";
    tenantId.value = a?.tenantId ?? "";
    appOnly.value = a?.appOnly ?? false;
    authorizeUrl.value = a?.authorizeUrl ?? "";
    tokenUrl.value = a?.tokenUrl ?? "";
    revokeUrl.value = a?.revokeUrl ?? "";
    scopes.value = (a?.scopes ?? []).join(" ");
    enabled.value = a?.enabled ?? true;
  },
);

const close = (): void => {
  emit("update:modelValue", false);
};

const submit = async (): Promise<void> => {
  const ok = await form.value?.validate();
  if (!ok?.valid) return;
  const scopeList = scopes.value
    .split(/\s+/)
    .map((s) => s.trim())
    .filter(Boolean);
  if (isEdit.value) {
    const body: OauthAppPatch = {
      name: name.value.trim(),
      clientId: clientId.value.trim(),
      enabled: enabled.value,
      scopes: scopeList,
    };
    if (clientSecret.value) body.clientSecret = clientSecret.value;
    if (showGmailSA.value && serviceKey.value.trim()) {
      body.serviceKey = serviceKey.value.trim();
    }
    if (showOutlookTenant.value) {
      body.tenantId = tenantId.value.trim() || null;
      body.appOnly = appOnly.value;
    }
    if (showGenericUrls.value) {
      body.authorizeUrl = authorizeUrl.value.trim() || null;
      body.tokenUrl = tokenUrl.value.trim() || null;
      body.revokeUrl = revokeUrl.value.trim() || null;
    }
    emit("save", body);
    return;
  }
  const body: OauthAppCreate = {
    name: name.value.trim(),
    provider: provider.value,
    clientId: clientId.value.trim(),
    clientSecret: clientSecret.value,
    scopes: scopeList,
    enabled: enabled.value,
  };
  if (showGmailSA.value && serviceKey.value.trim()) {
    body.serviceKey = serviceKey.value.trim();
  }
  if (showOutlookTenant.value) {
    if (tenantId.value.trim()) body.tenantId = tenantId.value.trim();
    body.appOnly = appOnly.value;
  }
  if (showGenericUrls.value) {
    body.authorizeUrl = authorizeUrl.value.trim();
    body.tokenUrl = tokenUrl.value.trim();
    body.revokeUrl = revokeUrl.value.trim() || undefined;
  }
  emit("save", body);
};
</script>

<template>
  <AppDialog
    :model-value="modelValue"
    :title="title"
    :max-width="560"
    :loading="saving"
    persistent
    @update:model-value="emit('update:modelValue', $event)"
  >
    <v-form
      ref="form"
      @submit.prevent="submit"
    >
      <v-text-field
        v-model="name"
        :label="t('oauth.field_name')"
        :rules="required"
        class="mb-2"
      />
      <v-select
        v-if="!isEdit"
        v-model="provider"
        :items="providers"
        :label="t('oauth.field_provider')"
        :rules="required"
        class="mb-2"
      />
      <v-text-field
        v-model="clientId"
        :label="t('oauth.field_client_id')"
        :rules="required"
        class="mb-2"
      />
      <v-text-field
        v-model="clientSecret"
        :label="t('oauth.field_client_secret')"
        :rules="isEdit ? [] : required"
        :hint="isEdit ? t('oauth.field_client_secret_hint') : undefined"
        :persistent-hint="isEdit"
        :class="['mb-2', isEdit ? 'no-validation' : '']"
        type="password"
        autocomplete="new-password"
      />
      <v-textarea
        v-if="showGmailSA"
        v-model="serviceKey"
        :label="t('oauth.field_service_key')"
        :hint="isEdit && app?.hasServiceKey ? t('oauth.field_service_key_hint') : t('oauth.field_service_key_help')"
        class="mb-2 no-validation"
        rows="4"
        auto-grow
        persistent-hint
      />
      <v-text-field
        v-if="showOutlookTenant"
        v-model="tenantId"
        :label="t('oauth.field_tenant_id')"
        :hint="t('oauth.field_tenant_id_help')"
        class="mb-2 no-validation"
        persistent-hint
      />
      <v-switch
        v-if="showOutlookTenant"
        v-model="appOnly"
        :label="t('oauth.field_app_only')"
        color="primary"
        class="mb-2 no-validation"
        hide-details
      />
      <template v-if="showGenericUrls">
        <v-text-field
          v-model="authorizeUrl"
          :label="t('oauth.field_authorize')"
          :rules="required"
          class="mb-2"
        />
        <v-text-field
          v-model="tokenUrl"
          :label="t('oauth.field_token')"
          :rules="required"
          class="mb-2"
        />
        <v-text-field
          v-model="revokeUrl"
          :label="t('oauth.field_revoke')"
          class="mb-2 no-validation"
        />
      </template>
      <v-text-field
        v-model="scopes"
        :label="t('oauth.field_scopes')"
        :rules="showGenericUrls ? required : []"
        :class="['mb-2', showGenericUrls ? '' : 'no-validation']"
      />
      <v-switch
        v-model="enabled"
        :label="t('oauth.field_enabled')"
        color="success"
        class="no-validation"
        hide-details
      />
    </v-form>

    <template #footer>
      <v-btn
        @click="close"
      >
        {{ t("oauth.cancel") }}
      </v-btn>
      <v-btn
        :loading="saving"
        color="success"
        @click="submit"
      >
        {{ t("oauth.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
