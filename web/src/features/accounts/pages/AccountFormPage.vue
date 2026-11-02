<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { useDisplay } from "vuetify";
import { mdiContentSave, mdiShieldCheck } from "@mdi/js";
import type { PECDefaults } from "@/api/pec";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import { actionOf } from "@/constants/Actions";
import { required } from "@/constants/Validations";
import { useConfirm } from "@/composables/useConfirm";
import AccountMetadataEditor from "@/features/accounts/components/AccountMetadataEditor.vue";
import VerifyResults from "@/features/accounts/components/VerifyResults.vue";
import { useAccountForm } from "@/features/accounts/composables/useAccountForm";
import {
  identityRules,
  imapRules,
  localiseRule,
  smtpRules,
} from "@/features/accounts/helpers/accountForm";
import {
  SEND_POLICIES,
  TLS_OPTIONS,
  type SendPolicy,
} from "@/features/accounts/helpers/accountState";
import AccountOauthConnect from "@/features/oauth/components/AccountOauthConnect.vue";
import PecPresetBanner from "@/features/pec/components/PecPresetBanner.vue";
import {
  applyPecEnabled,
  applyPecInboxOnly,
  applyPecSendPolicy,
  type PecFormTarget,
} from "@/features/pec/helpers/presets";
import { LOCALES } from "@/app/locales";

const { t } = useI18n();
const router = useRouter();
const confirm = useConfirm();
const { mdAndUp } = useDisplay();
const saveAction = actionOf("save");
const verifyAction = actionOf("verify");

const {
  accountId,
  isEdit,
  form,
  step,
  stepCount,
  titleKey,
  loadError,
  saving,
  verifying,
  verifyResult,
  verifyError,
  verifyPassed,
  hasPassword,
  metadataErrors,
  metadataValid,
  isFirstLoad,
  isRefreshing,
  load,
  goNext,
  goPrev,
  runVerify,
  save,
} = useAccountForm();

onMounted(() => {
  void load();
});

const loc = localiseRule(t);
const rulesIdentity = {
  name: loc(identityRules.name),
  email: loc(identityRules.email),
};
const rulesImap = computed(() => ({
  host: loc(imapRules.host),
  port: loc(imapRules.port),
  user: loc(imapRules.user),
  pass: isEdit.value ? [] : required,
}));
const rulesSmtp = computed(() => ({
  host: loc(smtpRules.host),
  port: loc(smtpRules.port),
  user: loc(smtpRules.user),
  pass: isEdit.value ? [] : required,
}));

const identityForm = ref<HTMLFormElement | null>(null);
const imapForm = ref<HTMLFormElement | null>(null);
const smtpForm = ref<HTMLFormElement | null>(null);

const pecFormTarget = (): PecFormTarget => ({
  smtpPolicy: form.value.smtpPolicy,
  syncPaths: form.value.syncPaths,
  idlePaths: form.value.idlePaths,
  pecEnabled: form.value.pecEnabled,
});

const onPecEnable = (): void => {
  const target = pecFormTarget();
  applyPecEnabled(target);
  form.value.pecEnabled = target.pecEnabled;
};

const onPecInbox = (defaults: PECDefaults | null): void => {
  const target = pecFormTarget();
  applyPecInboxOnly(target, defaults);
  form.value.syncPaths = target.syncPaths;
  form.value.idlePaths = target.idlePaths;
};

const onPecPolicy = (defaults: PECDefaults | null): void => {
  const target = pecFormTarget();
  applyPecSendPolicy(target, defaults);
  form.value.smtpPolicy = (target.smtpPolicy as SendPolicy) || "atMostOnce";
};

const stepItems = computed(() => [
  { title: t("accounts.step_identity"), value: 1 },
  { title: t("accounts.step_imap"), value: 2 },
  { title: t("accounts.step_smtp"), value: 3 },
  { title: t("accounts.step_sync"), value: 4 },
  { title: t("accounts.step_verify"), value: 5 },
]);

const currentStepTitle = computed(
  () => stepItems.value.find((s) => s.value === step.value)?.title ?? "",
);

const stepProgressLabel = computed(() =>
  t("accounts.step_of", { current: step.value, total: stepCount }),
);

const tzItems = computed(() => {
  try {
    return Intl.supportedValuesOf("timeZone").map((z) => ({ title: z, value: z }));
  } catch {
    return [{ title: "UTC", value: "UTC" }];
  }
});

const localeItems = computed(() =>
  LOCALES.map((l) => ({ title: l.nativeName, value: l.code })),
);

const tlsItems = computed(() =>
  TLS_OPTIONS.map((v) => ({ title: t(`accounts.tls_${v}`), value: v })),
);

const policyItems = computed(() =>
  SEND_POLICIES.map((v) => ({ title: t(`accounts.policy_${v}`), value: v })),
);

const poolStrategyItems = computed(() =>
  ["random", "round-robin", "hash-by-account"].map((v) => ({
    title: t(`accounts.strategy_${v.replace(/-/g, "_")}`),
    value: v,
  })),
);

const policyHint = computed(() =>
  form.value.smtpPolicy === "atMostOnce"
    ? t("accounts.policy_atMostOnce_hint")
    : t("accounts.policy_standard_hint"),
);

const validateEl = async (el: HTMLFormElement | null): Promise<boolean> => {
  const formEl = el as unknown as { validate?: () => Promise<{ valid: boolean }> };
  if (formEl?.validate) {
    const r = await formEl.validate();
    return r.valid;
  }
  return true;
};

const onNext = async (): Promise<void> => {
  if (step.value === 1 && !(await validateEl(identityForm.value))) return;
  if (step.value === 1 && !metadataValid.value) return;
  if (step.value === 2 && !(await validateEl(imapForm.value))) return;
  if (step.value === 3 && !(await validateEl(smtpForm.value))) return;
  goNext();
};

const onVerify = async (): Promise<void> => {
  await runVerify();
};

const onSave = async (): Promise<void> => {
  if (verifyPassed.value) {
    await save();
    return;
  }
  const ok = await confirm({
    title: t("accounts.save_unverified_title"),
    body: t("accounts.save_unverified_body"),
    variant: "warning",
    confirmText: t("accounts.btn_save"),
  });
  if (!ok) return;
  await save({ skipVerify: true });
};
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t(titleKey) }}
      </h1>
    </div>

    <LoadingSkeleton
      v-if="isFirstLoad && isEdit"
      :rows="8"
      shape="form"
    />

    <div
      v-else
      class="position-relative"
    >
      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load"
          >
            {{ t("accounts.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <template v-else>
        <AppCard content-class="pa-0 overflow-hidden">
          <v-stepper
            v-model="step"
            :mobile="!mdAndUp"
            :alt-labels="mdAndUp"
            class="bg-transparent"
            flat
          >
            <v-stepper-header class="border-b">
              <template
                v-for="(item, i) in stepItems"
                :key="item.value"
              >
                <v-stepper-item
                  :value="item.value"
                  :title="item.title"
                  color="primary"
                />
                <v-divider
                  v-if="i < stepItems.length - 1"
                />
              </template>
            </v-stepper-header>

            <div
              v-if="!mdAndUp"
              class="px-4 pt-3 pb-1"
            >
              <div class="text-title-small font-weight-bold">
                {{ currentStepTitle }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{ stepProgressLabel }}
              </div>
            </div>

            <div class="pa-4 pb-0">
              <PecPresetBanner
                :email="form.email"
                @apply-enabled="onPecEnable"
                @apply-inbox="onPecInbox"
                @apply-policy="onPecPolicy"
              />

              <v-chip
                v-if="form.pecEnabled"
                data-testid="pec-enabled-pending"
                class="mb-4"
                color="success"
                label
              >
                {{ t("pec.preset_enable") }}
              </v-chip>
            </div>

            <v-stepper-window>
              <v-stepper-window-item :value="1">
                <div class="pa-4 pt-2">
                  <v-form ref="identityForm">
                    <v-row>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.name"
                          :label="t('accounts.field_name')"
                          :rules="rulesIdentity.name"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.email"
                          :label="t('accounts.field_email')"
                          :rules="rulesIdentity.email"
                          type="email"
                          autocomplete="email"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.externalId"
                          :label="t('accounts.field_external_id')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="3"
                      >
                        <AutocompleteSearch
                          v-model="form.tz"
                          :items="tzItems"
                          :label="t('accounts.field_tz')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="3"
                      >
                        <AutocompleteSearch
                          v-model="form.locale"
                          :items="localeItems"
                          :label="t('accounts.field_locale')"
                          class="no-validation"
                          hide-details
                        />
                      </v-col>
                      <v-col cols="12">
                        <AccountMetadataEditor
                          v-model="form.metadata"
                          :server-errors="metadataErrors"
                          :allow-null="!isEdit"
                        />
                      </v-col>
                    </v-row>
                  </v-form>
                </div>
              </v-stepper-window-item>

              <v-stepper-window-item :value="2">
                <div class="pa-4 pt-2">
                  <v-form ref="imapForm">
                    <v-row>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapHost"
                          :label="t('accounts.field_host')"
                          :rules="rulesImap.host"
                        />
                      </v-col>
                      <v-col
                        cols="6"
                        md="3"
                      >
                        <v-text-field
                          v-model.number="form.imapPort"
                          :label="t('accounts.field_port')"
                          :rules="rulesImap.port"
                          type="number"
                        />
                      </v-col>
                      <v-col
                        cols="6"
                        md="3"
                      >
                        <v-select
                          v-model="form.imapTls"
                          :items="tlsItems"
                          :label="t('accounts.field_tls')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapUser"
                          :label="t('accounts.field_user')"
                          :rules="rulesImap.user"
                          autocomplete="username"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapPass"
                          :label="t('accounts.field_pass')"
                          :rules="rulesImap.pass"
                          :placeholder="isEdit && hasPassword.imap ? t('accounts.field_pass_unchanged') : undefined"
                          :hint="isEdit ? t('accounts.field_pass_hint_edit') : undefined"
                          :persistent-hint="isEdit"
                          :class="isEdit ? 'no-validation' : undefined"
                          type="password"
                          autocomplete="new-password"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapIdName"
                          :label="t('accounts.field_id_name')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapIdVersion"
                          :label="t('accounts.field_id_version')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapIdVendor"
                          :label="t('accounts.field_id_vendor')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.imapIdSupportUrl"
                          :label="t('accounts.field_id_support_url')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col cols="12">
                        <v-switch
                          v-model="form.imapDisableRev2"
                          :label="t('accounts.field_disable_rev2')"
                          :hint="t('accounts.disable_rev2_hint')"
                          class="no-validation"
                          persistent-hint
                        />
                      </v-col>
                    </v-row>
                  </v-form>
                </div>
              </v-stepper-window-item>

              <v-stepper-window-item :value="3">
                <div class="pa-4 pt-2">
                  <v-form ref="smtpForm">
                    <v-row>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.smtpHost"
                          :label="t('accounts.field_host')"
                          :rules="rulesSmtp.host"
                        />
                      </v-col>
                      <v-col
                        cols="6"
                        md="3"
                      >
                        <v-text-field
                          v-model.number="form.smtpPort"
                          :label="t('accounts.field_port')"
                          :rules="rulesSmtp.port"
                          type="number"
                        />
                      </v-col>
                      <v-col
                        cols="6"
                        md="3"
                      >
                        <v-select
                          v-model="form.smtpTls"
                          :items="tlsItems"
                          :label="t('accounts.field_tls')"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.smtpUser"
                          :label="t('accounts.field_user')"
                          :rules="rulesSmtp.user"
                          autocomplete="username"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.smtpPass"
                          :label="t('accounts.field_pass')"
                          :rules="rulesSmtp.pass"
                          :placeholder="isEdit && hasPassword.smtp ? t('accounts.field_pass_unchanged') : undefined"
                          :hint="isEdit ? t('accounts.field_pass_hint_edit') : undefined"
                          :persistent-hint="isEdit"
                          :class="isEdit ? 'no-validation' : undefined"
                          type="password"
                          autocomplete="new-password"
                        />
                      </v-col>
                      <v-col cols="12">
                        <v-select
                          v-model="form.smtpPolicy"
                          :items="policyItems"
                          :label="t('accounts.field_policy')"
                          :hint="policyHint"
                          class="no-validation mb-2"
                          persistent-hint
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model.number="form.smtpMaxAttempts"
                          :label="t('accounts.field_max_attempts')"
                          type="number"
                          class="no-validation"
                        />
                      </v-col>
                      <v-col
                        cols="12"
                        md="6"
                      >
                        <v-text-field
                          v-model="form.smtpBackoffBase"
                          :label="t('accounts.field_backoff')"
                          class="no-validation"
                        />
                      </v-col>
                    </v-row>
                  </v-form>
                </div>
              </v-stepper-window-item>

              <v-stepper-window-item :value="4">
                <div class="pa-4 pt-2">
                  <v-row>
                    <v-col cols="12">
                      <v-text-field
                        v-model="form.syncPaths"
                        :label="t('accounts.field_paths')"
                        :hint="t('accounts.paths_hint')"
                        class="no-validation"
                        persistent-hint
                      />
                    </v-col>
                    <v-col cols="12">
                      <v-text-field
                        v-model="form.idlePaths"
                        :label="t('accounts.field_idle_paths')"
                        class="no-validation"
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.notifyFrom"
                        :label="t('accounts.field_notify_from')"
                        type="datetime-local"
                        class="no-validation"
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.syncFrom"
                        :label="t('accounts.field_sync_from')"
                        type="datetime-local"
                        class="no-validation"
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.resyncDelay"
                        :label="t('accounts.field_resync_delay')"
                        class="no-validation"
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.maxIdle"
                        :label="t('accounts.field_max_idle')"
                        class="no-validation"
                      />
                    </v-col>
                  </v-row>
                </div>
              </v-stepper-window-item>

              <v-stepper-window-item :value="5">
                <div class="pa-4 pt-2">
                  <p class="text-body-2 text-medium-emphasis mb-4">
                    {{ t("accounts.network_intro") }}
                  </p>
                  <v-row class="mb-2">
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.netProxy"
                        :label="t('accounts.field_proxy')"
                        :hint="t('accounts.proxy_hint')"
                        class="no-validation"
                        persistent-hint
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.netLocalAddress"
                        :label="t('accounts.field_local_address')"
                        :hint="t('accounts.local_address_hint')"
                        class="no-validation"
                        persistent-hint
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.netEhloName"
                        :label="t('accounts.field_ehlo_name')"
                        class="no-validation"
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-text-field
                        v-model="form.netLocalAddresses"
                        :label="t('accounts.field_local_addresses')"
                        :hint="t('accounts.local_addresses_hint')"
                        class="no-validation"
                        persistent-hint
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-select
                        v-model="form.netImapStrategy"
                        :items="poolStrategyItems"
                        :label="t('accounts.field_imap_strategy')"
                        class="no-validation"
                        clearable
                      />
                    </v-col>
                    <v-col
                      cols="12"
                      md="6"
                    >
                      <v-select
                        v-model="form.netSmtpStrategy"
                        :items="poolStrategyItems"
                        :label="t('accounts.field_smtp_strategy')"
                        class="no-validation"
                        clearable
                      />
                    </v-col>
                  </v-row>

                  <v-switch
                    v-model="form.ignoreCertErrors"
                    :label="t('accounts.field_ignore_cert')"
                    color="warning"
                    class="no-validation mb-2"
                  />
                  <v-alert
                    v-if="form.ignoreCertErrors"
                    type="warning"
                    class="mb-4"
                  >
                    {{ t("accounts.ignore_cert_warn") }}
                  </v-alert>

                  <div class="mb-4">
                    <v-btn
                      :loading="verifying"
                      :color="verifyAction.color"
                      :prepend-icon="mdiShieldCheck"
                      @click="onVerify"
                    >
                      {{ t("accounts.btn_verify") }}
                    </v-btn>
                  </div>

                  <VerifyResults
                    :result="verifyResult"
                    :error="verifyError"
                    :loading="verifying"
                  />
                </div>
              </v-stepper-window-item>
            </v-stepper-window>

            <template #actions>
              <div class="d-flex flex-wrap justify-space-between ga-2 px-4 pb-4 pt-2 w-100">
                <div class="d-flex flex-wrap ga-2">
                  <v-btn
                    v-if="step > 1"
                    @click="goPrev"
                  >
                    {{ t("accounts.btn_back") }}
                  </v-btn>
                  <v-btn
                    @click="router.back()"
                  >
                    {{ t("accounts.btn_cancel") }}
                  </v-btn>
                </div>
                <div class="d-flex ga-2">
                  <v-btn
                    v-if="step < stepCount"
                    color="primary"
                    @click="onNext"
                  >
                    {{ t("accounts.btn_next") }}
                  </v-btn>
                  <v-btn
                    v-else
                    :loading="saving"
                    :color="saveAction.color"
                    :prepend-icon="mdiContentSave"
                    @click="onSave"
                  >
                    {{ t("accounts.btn_save") }}
                  </v-btn>
                </div>
              </div>
            </template>
          </v-stepper>
        </AppCard>

        <AccountOauthConnect
          v-if="isEdit && accountId"
          :account-id="accountId"
          class="mt-4"
        />
      </template>

      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>
