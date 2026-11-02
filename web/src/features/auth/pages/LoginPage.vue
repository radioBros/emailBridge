<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  mdiEmailOutline,
  mdiFingerprint,
  mdiLockOutline,
  mdiLoginVariant,
  mdiShieldKey,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppLogo from "@/components/common/AppLogo.vue";
import ThemeToggle from "@/components/common/ThemeToggle.vue";
import LanguageMenu from "@/app/components/LanguageMenu.vue";
import AuthErrorAlert from "@/features/auth/components/AuthErrorAlert.vue";
import { useLoginFlow } from "@/features/auth/composables/useLoginFlow";
import { useSessionStore } from "@/stores/session";
import { combine, email as emailRules, required } from "@/constants/Validations";
import { actionOf } from "@/constants/Actions";
import { listPublicOidcProviders, oidcStartUrl, type OidcPublicProvider } from "@/api/oidc";

const { t } = useI18n();
const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const mfaFormRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const oidcProviders = ref<OidcPublicProvider[]>([]);
const ssoErrorDismissed = ref(false);
const ssoError = computed(
  () => !ssoErrorDismissed.value && route.query.sso_error === "1",
);

const {
  step,
  email,
  password,
  mfaCode,
  mfaTab,
  loading,
  passkeyLoading,
  otpSending,
  otpCooldownSec,
  error,
  canSubmitCredentials,
  canSubmitMfa,
  submitCredentials,
  submitMfa,
  sendOtp,
  submitPasskey,
  backToCredentials,
  enterMfa,
} = useLoginFlow();

const verifyAction = actionOf("verify");
const saveAction = actionOf("save");

const redirectTo = computed(() => {
  const r = route.query.redirect;
  return typeof r === "string" && r.startsWith("/") ? r : "/";
});

const finishLogin = async (): Promise<void> => {
  await session.loadMe();
  if (session.needsEnrolment) {
    await router.replace({ name: "enrolment" });
    return;
  }
  await router.replace(redirectTo.value);
};

onMounted(() => {
  if (route.query.mfa === "1") enterMfa();
  void listPublicOidcProviders()
    .then((list) => {
      oidcProviders.value = list;
    })
    .catch(() => {
      oidcProviders.value = [];
    });
});

const dismissSsoError = (): void => {
  ssoErrorDismissed.value = true;
  if (route.query.sso_error) {
    const q = { ...route.query };
    delete q.sso_error;
    void router.replace({ query: q });
  }
};

const startSso = (providerId: string): void => {
  window.location.assign(oidcStartUrl(providerId, redirectTo.value));
};

watch(
  () => session.isAuthenticated,
  async (ok) => {
    if (!ok || session.isPending || loading.value || passkeyLoading.value) return;
    if (step.value !== "credentials") return;
    await session.loadMe();
    if (session.needsEnrolment) await router.replace({ name: "enrolment" });
    else if (!route.query.mfa) await router.replace(redirectTo.value);
  },
);

const onCredentials = async (): Promise<void> => {
  const result = await formRef.value?.validate();
  if (result && !result.valid) return;
  const outcome = await submitCredentials();
  if (outcome === "ok") await finishLogin();
};

const onMfa = async (): Promise<void> => {
  const result = await mfaFormRef.value?.validate();
  if (result && !result.valid) return;
  const outcome = await submitMfa();
  if (outcome === "ok") await finishLogin();
};

const onPasskey = async (): Promise<void> => {
  const outcome = await submitPasskey();
  if (outcome === "ok") await finishLogin();
};
</script>

<template>
  <AppCard
    content-class="pa-6 pa-md-8"
    elevated
  >
    <template #header>
      <div class="d-flex align-center ga-3">
        <AppLogo :size="48" />
        <div class="min-w-0 flex-grow-1">
          <div class="text-title-large font-weight-bold">
            {{ t("auth.brand") }}
          </div>
          <div class="text-body-small text-medium-emphasis">
            {{ step === "mfa" ? t("auth.mfa_subtitle") : t("auth.login_subtitle") }}
          </div>
        </div>
        <ThemeToggle />
        <LanguageMenu />
      </div>
    </template>

    <h1 class="text-h5 font-weight-bold mb-4">
      {{ step === "mfa" ? t("auth.mfa_title") : t("auth.login_title") }}
    </h1>

    <AuthErrorAlert :error="error" />

    <v-alert
      v-if="ssoError"
      type="error"
      class="mb-4"
      role="alert"
      closable
      @click:close="dismissSsoError"
    >
      {{ t("auth.sso_error") }}
    </v-alert>

    <template v-if="step === 'credentials'">
      <v-btn
        :loading="passkeyLoading"
        :prepend-icon="mdiFingerprint"
        color="primary"
        class="mb-4"
        block
        @click="onPasskey"
      >
        {{ t("auth.sign_in_passkey") }}
      </v-btn>

      <div class="text-center text-body-small text-medium-emphasis mb-4">
        {{ t("auth.or_password") }}
      </div>

      <v-form
        ref="formRef"
        @submit.prevent="onCredentials"
      >
        <v-text-field
          v-model="email"
          :label="t('auth.email')"
          :rules="combine(required, emailRules)"
          :prepend-inner-icon="mdiEmailOutline"
          type="email"
          name="username"
          autocomplete="username"
          class="mb-2"
          autofocus
        />
        <v-text-field
          v-model="password"
          :label="t('auth.password')"
          :rules="required"
          :prepend-inner-icon="mdiLockOutline"
          type="password"
          name="password"
          autocomplete="current-password"
          class="mb-4"
        />
        <v-btn
          :loading="loading"
          :disabled="!canSubmitCredentials"
          :color="saveAction.color"
          type="submit"
          data-testid="login-submit"
          block
        >
          {{ t("auth.sign_in") }}
        </v-btn>
      </v-form>

      <template v-if="oidcProviders.length">
        <div class="text-center text-body-small text-medium-emphasis my-4">
          {{ t("auth.or_sso") }}
        </div>
        <v-btn
          v-for="p in oidcProviders"
          :key="p.id"
          :prepend-icon="mdiLoginVariant"
          color="info"
          class="mb-2"
          block
          @click="startSso(p.id)"
        >
          {{ p.buttonLabel }}
        </v-btn>
      </template>
    </template>

    <template v-else>
      <v-btn-toggle
        v-model="mfaTab"
        class="auth-mfa-toggle mb-4 w-100"
        color="primary"
        divided
        mandatory
      >
        <v-btn
          :text="t('auth.mfa_totp')"
          class="flex-grow-1 text-none"
          value="totp"
        />
        <v-btn
          :text="t('auth.mfa_otp')"
          class="flex-grow-1 text-none"
          value="otp"
        />
        <v-btn
          :text="t('auth.mfa_backup')"
          class="flex-grow-1 text-none"
          value="backup"
        />
      </v-btn-toggle>

      <v-form
        ref="mfaFormRef"
        @submit.prevent="onMfa"
      >
        <div
          v-if="mfaTab === 'otp'"
          class="mb-3"
        >
          <v-btn
            :loading="otpSending"
            :disabled="otpCooldownSec > 0"
            :color="verifyAction.color"
            block
            @click="sendOtp"
          >
            <template v-if="otpCooldownSec > 0">
              {{ t("auth.mfa_resend_in", { sec: otpCooldownSec }) }}
            </template>
            <template v-else>
              {{ t("auth.mfa_send_code") }}
            </template>
          </v-btn>
        </div>

        <v-text-field
          v-model="mfaCode"
          :label="mfaTab === 'backup' ? t('auth.mfa_backup_code') : t('auth.mfa_code')"
          :rules="required"
          :prepend-inner-icon="mdiShieldKey"
          autocomplete="one-time-code"
          inputmode="numeric"
          data-testid="mfa-code"
          class="mb-4"
          autofocus
        />

        <v-btn
          :loading="loading"
          :disabled="!canSubmitMfa"
          :color="verifyAction.color"
          type="submit"
          data-testid="mfa-verify"
          class="mb-2"
          block
        >
          {{ t("auth.mfa_verify") }}
        </v-btn>
        <v-btn
          block
          @click="backToCredentials"
        >
          {{ t("auth.mfa_back") }}
        </v-btn>
      </v-form>
    </template>
  </AppCard>
</template>

<style scoped lang="scss">
.auth-mfa-toggle {
  display: flex;
  width: 100%;

  :deep(.v-btn) {
    color: rgb(var(--v-theme-on-surface-variant));
    opacity: 1 !important;
  }

  :deep(.v-btn.v-btn--active),
  :deep(.v-btn[aria-pressed="true"]) {
    color: rgb(var(--v-theme-primary));
    background-color: rgb(var(--v-theme-primary) / 16%);
  }
}
</style>
