<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import {
  mdiCellphoneKey,
  mdiContentCopy,
  mdiFingerprint,
  mdiShieldCheck,
  mdiShieldKey,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import BackupCodesPanel from "@/features/auth/components/BackupCodesPanel.vue";
import TotpQr from "@/features/auth/components/TotpQr.vue";
import { useEnrolmentFlow } from "@/features/auth/composables/useEnrolmentFlow";
import { useSessionStore } from "@/stores/session";
import { useNotifyStore } from "@/stores/notify";
import { required } from "@/constants/Validations";
import { actionOf } from "@/constants/Actions";

const { t, te } = useI18n();
const router = useRouter();
const session = useSessionStore();
const notify = useNotifyStore();

const {
  phase,
  password,
  totpCode,
  passkeyName,
  setup,
  backupCodes,
  loading,
  errorCode,
  errorDetail,
  choosePasskey,
  chooseTotp,
  startTotp,
  confirmTotp,
  registerPasskey,
  finishBackupCodes,
} = useEnrolmentFlow();

const createAction = actionOf("create");
const verifyAction = actionOf("verify");
const copyAction = actionOf("copy");

const errorMessage = (): string => {
  if (!errorCode.value) return "";
  const key = `auth.error_${errorCode.value}`;
  if (te(key)) return t(key);
  return errorDetail.value || t("auth.error_generic");
};

const onPasskey = async (): Promise<void> => {
  const ok = await registerPasskey();
  if (ok) {
    session.markEnrolled();
    await router.replace("/");
  }
};

const onTotpStart = async (): Promise<void> => {
  await startTotp();
};

const onTotpConfirm = async (): Promise<void> => {
  const ok = await confirmTotp();
  if (ok) session.markEnrolled();
};

const onBackupDone = async (): Promise<void> => {
  finishBackupCodes();
  session.markEnrolled();
  await router.replace("/");
};

const onDoneContinue = async (): Promise<void> => {
  session.markEnrolled();
  await router.replace("/");
};

const copySecret = async (): Promise<void> => {
  if (!setup.value?.secret) return;
  await navigator.clipboard.writeText(setup.value.secret);
  notify.push({ message: t("auth.enrol_manual_secret"), color: "success" });
};
</script>

<template>
  <AppCard
    content-class="pa-6 pa-md-8"
    elevated
  >
    <template #header>
      <div class="d-flex align-center ga-3">
        <v-avatar-lead
          color="success"
        >
          <v-icon
            :icon="mdiShieldCheck"
          />
        </v-avatar-lead>
        <div class="min-w-0">
          <div class="text-title-large font-weight-bold">
            {{ t("auth.enrol_title") }}
          </div>
          <div class="text-body-small text-medium-emphasis">
            {{ t("auth.enrol_subtitle") }}
          </div>
        </div>
      </div>
    </template>

    <v-alert
      v-if="errorCode"
      type="error"
      class="mb-4"
      role="alert"
    >
      {{ errorMessage() }}
    </v-alert>

    <template v-if="phase === 'choose'">
      <v-row>
        <v-col
          cols="12"
          sm="6"
        >
          <AppCard
            content-class="pa-4"
            selectable
            @click="choosePasskey"
          >
            <div class="d-flex flex-column align-center text-center ga-3">
              <v-avatar-hero
                color="primary"
              >
                <v-icon
                  :icon="mdiFingerprint"
                />
              </v-avatar-hero>
              <div class="text-title-medium font-weight-bold">
                {{ t("auth.enrol_passkey") }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{ t("auth.enrol_passkey_hint") }}
              </div>
            </div>
          </AppCard>
        </v-col>
        <v-col
          cols="12"
          sm="6"
        >
          <AppCard
            content-class="pa-4"
            selectable
            @click="chooseTotp"
          >
            <div class="d-flex flex-column align-center text-center ga-3">
              <v-avatar-hero
                color="info"
              >
                <v-icon
                  :icon="mdiCellphoneKey"
                />
              </v-avatar-hero>
              <div class="text-title-medium font-weight-bold">
                {{ t("auth.enrol_totp") }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{ t("auth.enrol_totp_hint") }}
              </div>
            </div>
          </AppCard>
        </v-col>
      </v-row>
    </template>

    <template v-else-if="phase === 'passkey'">
      <v-text-field
        v-model="passkeyName"
        :label="t('auth.enrol_passkey_name')"
        :hint="t('auth.enrol_passkey_name_hint')"
        class="mb-4 no-validation"
        persistent-hint
      />
      <v-btn
        :loading="loading"
        :prepend-icon="mdiFingerprint"
        :color="createAction.color"
        block
        @click="onPasskey"
      >
        {{ t("auth.enrol_register_passkey") }}
      </v-btn>
    </template>

    <template v-else-if="phase === 'totp'">
      <template v-if="!setup">
        <v-text-field
          v-model="password"
          :label="t('auth.enrol_password_confirm')"
          :rules="required"
          type="password"
          autocomplete="current-password"
          class="mb-4"
        />
        <v-btn
          :loading="loading"
          :disabled="!password"
          :color="verifyAction.color"
          block
          @click="onTotpStart"
        >
          {{ t("auth.enrol_show_qr") }}
        </v-btn>
      </template>
      <template v-else>
        <TotpQr
          :uri="setup.uri"
          class="mb-4"
        />
        <div class="text-body-small text-medium-emphasis mb-1">
          {{ t("auth.enrol_manual_secret") }}
        </div>
        <div class="d-flex align-center ga-2 mb-4">
          <code class="flex-grow-1 text-body-medium text-break min-w-0">
            {{ setup.secret }}
          </code>
          <v-btn-affix
            v-tippy="t('auth.enrol_copy_codes')"
            :icon="mdiContentCopy"
            :color="copyAction.color"
            :aria-label="t('auth.enrol_copy_codes')"
            @click="copySecret"
          />
        </div>
        <v-text-field
          v-model="totpCode"
          :label="t('auth.enrol_totp_code')"
          :rules="required"
          :prepend-inner-icon="mdiShieldKey"
          autocomplete="one-time-code"
          inputmode="numeric"
          class="mb-4"
        />
        <v-btn
          :loading="loading"
          :disabled="!totpCode"
          :color="verifyAction.color"
          block
          @click="onTotpConfirm"
        >
          {{ t("auth.enrol_confirm_totp") }}
        </v-btn>
      </template>
    </template>

    <template v-else-if="phase === 'backup_codes'">
      <h2 class="text-title-large font-weight-bold mb-2">
        {{ t("auth.enrol_backup_title") }}
      </h2>
      <BackupCodesPanel
        :codes="backupCodes"
        @done="onBackupDone"
      />
    </template>

    <template v-else>
      <div class="text-center py-4">
        <v-avatar-hero
          color="success"
          class="mb-4"
        >
          <v-icon
            :icon="mdiShieldCheck"
          />
        </v-avatar-hero>
        <h2 class="text-title-large font-weight-bold mb-2">
          {{ t("auth.enrol_done_title") }}
        </h2>
        <p class="text-body-medium text-medium-emphasis mb-4">
          {{ t("auth.enrol_done_body") }}
        </p>
        <v-btn
          color="success"
          block
          @click="onDoneContinue"
        >
          {{ t("auth.enrol_continue") }}
        </v-btn>
      </div>
    </template>
  </AppCard>
</template>
