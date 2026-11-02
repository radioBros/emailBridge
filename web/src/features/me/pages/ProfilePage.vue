<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import {
  mdiAccountCircle,
  mdiDelete,
  mdiFingerprint,
  mdiKeyRemove,
  mdiLogout,
  mdiPlus,
  mdiShieldKey,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import { useProfile } from "@/features/me/composables/useProfile";
import { useConfirm } from "@/composables/useConfirm";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useSessionStore } from "@/stores/session";
import { actionOf } from "@/constants/Actions";
import {
  finalizeTotpSetup,
  initiateTotpSetup,
  type TotpSetup,
} from "@/api/authFlow";
import TotpQr from "@/features/auth/components/TotpQr.vue";
import BackupCodesPanel from "@/features/auth/components/BackupCodesPanel.vue";
import NotificationPreferencesCard from "@/features/notifications/components/NotificationPreferencesCard.vue";
import { required } from "@/constants/Validations";
import { LOCALES } from "@/app/locales";

const { t } = useI18n();
const router = useRouter();
const session = useSessionStore();
const confirm = useConfirm();
const saveAction = actionOf("save");
const createAction = actionOf("create");
const deleteAction = actionOf("delete");
const revokeAction = actionOf("revoke");

const {
  name,
  email,
  localeChoice,
  themeChoice,
  passkeys,
  sessions,
  otherSessionCount,
  totpEnabled,
  loadError,
  saving,
  passkeyName,
  totpPassword,
  mutating,
  isFirstLoad,
  isRefreshing,
  load,
  saveBasics,
  addPasskey,
  removePasskey,
  disableAuthenticator,
  revokeSession,
  revokeOthers,
} = useProfile();

const totpSetupOpen = ref(false);
const totpSetup = ref<TotpSetup | null>(null);
const totpCode = ref("");
const totpBackup = ref<string[] | null>(null);
const totpBusy = ref(false);

onMounted(() => {
  void load();
});

const localeItems = computed(() =>
  LOCALES.map((l) => ({ title: l.nativeName, value: l.code })),
);

const themeItems = computed(() => [
  { title: t("me.theme_light"), value: "light" },
  { title: t("me.theme_dark"), value: "dark" },
  { title: t("me.theme_system"), value: "system" },
]);

const onRemovePasskey = async (id: string): Promise<void> => {
  const row = passkeys.value.find((p) => p.id === id);
  if (!row) return;
  const ok = await confirm({
    title: t("me.passkey_remove_title"),
    body: t("me.passkey_remove_body"),
    variant: "danger",
    confirmText: t("me.passkey_remove"),
  });
  if (!ok) return;
  await removePasskey(row);
};

const onDisableTotp = async (): Promise<void> => {
  const ok = await confirm({
    title: t("me.totp_disable_title"),
    body: t("me.totp_disable_body"),
    variant: "warning",
    confirmText: t("me.totp_disable"),
  });
  if (!ok) return;
  await disableAuthenticator();
};

const onRevokeSession = async (id: string): Promise<void> => {
  const ok = await confirm({
    title: t("me.session_revoke_title"),
    body: t("me.session_revoke_body"),
    variant: "danger",
    confirmText: t("me.session_revoke"),
  });
  if (!ok) return;
  await revokeSession(id);
};

const onRevokeOthers = async (): Promise<void> => {
  const ok = await confirm({
    title: t("me.sessions_revoke_title"),
    body: t("me.sessions_revoke_body"),
    variant: "danger",
    confirmText: t("me.sessions_revoke_others"),
  });
  if (!ok) return;
  await revokeOthers();
};

const notify = useNotifyStore();
const { messageOf } = useProblem();

const onSignOut = async (): Promise<void> => {
  try {
    await session.signOut();
    await router.push("/login");
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  }
};

// A failed call (wrong or missing password, expired code) used to throw unhandled, so the button
// looked dead. Every failure now surfaces as a translated error.
const reportTotpError = (err: unknown): void => {
  notify.push({ message: messageOf(err), color: "error" });
};

const startTotpSetup = async (): Promise<void> => {
  if (!totpPassword.value || totpBusy.value) return;
  totpBusy.value = true;
  try {
    totpSetup.value = await initiateTotpSetup(totpPassword.value);
    totpSetupOpen.value = true;
  } catch (err) {
    reportTotpError(err);
  } finally {
    totpBusy.value = false;
  }
};

const confirmTotpSetup = async (): Promise<void> => {
  if (!totpCode.value || totpBusy.value) return;
  totpBusy.value = true;
  try {
    totpBackup.value = await finalizeTotpSetup(totpCode.value);
    totpEnabled.value = true;
  } catch (err) {
    reportTotpError(err);
  } finally {
    totpBusy.value = false;
  }
};

const closeTotpBackup = (): void => {
  totpBackup.value = null;
  totpSetup.value = null;
  totpSetupOpen.value = false;
  totpCode.value = "";
  totpPassword.value = "";
};
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t("me.title") }}
      </h1>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("me.subtitle") }}
      </p>
    </div>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
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
            {{ t("me.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <v-row>
        <v-col
          cols="12"
          md="6"
        >
          <AppCard class="mb-4">
            <template #header>
              <div class="d-flex align-center ga-3">
                <v-avatar
                  color="primary"
                >
                  <v-icon
                    :icon="mdiAccountCircle"
                  />
                </v-avatar>
                <span class="text-title-medium font-weight-bold">
                  {{ t("me.basics") }}
                </span>
              </div>
            </template>

            <v-text-field
              v-model="name"
              :label="t('me.name')"
              :disabled="saving"
              class="mb-2 no-validation"
            />
            <v-text-field
              :model-value="email"
              :label="t('me.email')"
              class="mb-2 no-validation"
              readonly
              disabled
            />
            <AutocompleteSearch
              v-model="localeChoice"
              :items="localeItems"
              :label="t('me.locale')"
              :disabled="saving"
              class="mb-2 no-validation"
              hide-details
            />
            <v-select
              v-model="themeChoice"
              :items="themeItems"
              :label="t('me.theme')"
              :disabled="saving"
              class="mb-4 no-validation"
            />
            <v-btn
              :loading="saving"
              :color="saveAction.color"
              @click="saveBasics"
            >
              {{ t("me.save") }}
            </v-btn>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
          md="6"
        >
          <AppCard class="mb-4">
            <template #header>
              <div class="d-flex align-center ga-3">
                <v-avatar
                  color="info"
                >
                  <v-icon
                    :icon="mdiFingerprint"
                  />
                </v-avatar>
                <span class="text-title-medium font-weight-bold">
                  {{ t("me.passkeys") }}
                </span>
              </div>
            </template>

            <div
              v-if="!passkeys.length"
              class="text-body-medium text-medium-emphasis mb-4"
            >
              {{ t("me.passkeys_empty") }}
            </div>
            <v-list
              v-else
              class="mb-4 bg-transparent"
            >
              <v-list-item
                v-for="pk in passkeys"
                :key="pk.id"
              >
                <v-list-item-title>
                  {{ pk.name }}
                </v-list-item-title>
                <v-list-item-subtitle>
                  {{ t("me.passkey_created") }} · {{ pk.createdAt.slice(0, 10) }}
                </v-list-item-subtitle>
                <template #append>
                  <v-btn
                    v-tippy="t('me.passkey_remove')"
                    :icon="mdiDelete"
                    :color="deleteAction.color"
                    :aria-label="t('me.passkey_remove')"
                    @click="onRemovePasskey(pk.id)"
                  />
                </template>
              </v-list-item>
            </v-list>

            <v-text-field
              v-model="passkeyName"
              :label="t('me.passkey_name')"
              class="mb-2 no-validation"
            />
            <v-btn
              :loading="mutating"
              :prepend-icon="mdiPlus"
              :color="createAction.color"
              @click="addPasskey"
            >
              {{ t("me.passkey_add") }}
            </v-btn>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
          md="6"
        >
          <AppCard class="mb-4">
            <template #header>
              <div class="d-flex align-center ga-3">
                <v-avatar
                  color="secondary"
                >
                  <v-icon
                    :icon="mdiShieldKey"
                  />
                </v-avatar>
                <span class="text-title-medium font-weight-bold">
                  {{ t("me.totp") }}
                </span>
              </div>
            </template>

            <p class="text-body-medium mb-4">
              {{ totpEnabled ? t("me.totp_on") : t("me.totp_off") }}
            </p>

            <template v-if="totpBackup">
              <BackupCodesPanel
                :codes="totpBackup"
                @done="closeTotpBackup"
              />
            </template>
            <template v-else-if="totpSetupOpen && totpSetup">
              <TotpQr
                :uri="totpSetup.uri"
                class="mb-3"
              />
              <code class="d-block mb-3 text-body-small text-break">
                {{ totpSetup.secret }}
              </code>
              <v-text-field
                v-model="totpCode"
                :label="t('auth.enrol_totp_code')"
                :rules="required"
                autocomplete="one-time-code"
                class="mb-3"
                @keyup.enter="confirmTotpSetup"
              />
              <v-btn
                :loading="totpBusy"
                :disabled="!totpCode"
                :color="saveAction.color"
                @click="confirmTotpSetup"
              >
                {{ t("auth.enrol_confirm_totp") }}
              </v-btn>
            </template>
            <template v-else-if="!totpEnabled">
              <v-text-field
                v-model="totpPassword"
                :label="t('me.totp_password')"
                :rules="required"
                type="password"
                autocomplete="current-password"
                class="mb-3"
                @keyup.enter="startTotpSetup"
              />
              <v-btn
                :loading="totpBusy"
                :disabled="!totpPassword"
                :color="createAction.color"
                @click="startTotpSetup"
              >
                {{ t("me.totp_setup") }}
              </v-btn>
            </template>
            <template v-else>
              <v-text-field
                v-model="totpPassword"
                :label="t('me.totp_password')"
                type="password"
                autocomplete="current-password"
                class="mb-3 no-validation"
              />
              <v-btn
                :loading="mutating"
                :color="actionOf('disable').color"
                @click="onDisableTotp"
              >
                {{ t("me.totp_disable") }}
              </v-btn>
            </template>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
          md="6"
        >
          <AppCard class="mb-4">
            <template #header>
              <div class="d-flex align-center ga-3">
                <v-avatar
                  color="warning"
                >
                  <v-icon
                    :icon="mdiKeyRemove"
                  />
                </v-avatar>
                <span class="text-title-medium font-weight-bold">
                  {{ t("me.sessions") }}
                </span>
              </div>
            </template>

            <div
              v-if="!sessions.length"
              class="text-body-medium text-medium-emphasis mb-4"
            >
              {{ t("me.sessions_empty") }}
            </div>
            <v-list
              v-else
              class="mb-4 bg-transparent"
              lines="two"
            >
              <v-list-item
                v-for="s in sessions"
                :key="s.id"
              >
                <v-list-item-title>
                  {{ s.device }}
                  <v-chip
                    v-if="s.current"
                    color="success"
                    class="ms-2"
                  >
                    {{ t("me.session_current") }}
                  </v-chip>
                </v-list-item-title>
                <v-list-item-subtitle>
                  {{ s.ip }} · {{ s.method }} · {{ s.lastActiveLabel }}
                </v-list-item-subtitle>
                <template
                  v-if="!s.current"
                  #append
                >
                  <v-btn
                    :color="revokeAction.color"
                    :loading="mutating"
                    @click="onRevokeSession(s.id)"
                  >
                    {{ t("me.session_revoke") }}
                  </v-btn>
                </template>
              </v-list-item>
            </v-list>

            <div class="d-flex flex-wrap ga-2">
              <v-btn
                v-if="otherSessionCount > 0"
                :loading="mutating"
                :color="revokeAction.color"
                @click="onRevokeOthers"
              >
                {{ t("me.sessions_revoke_others") }}
              </v-btn>
              <v-btn
                :prepend-icon="mdiLogout"
                color="secondary"
                @click="onSignOut"
              >
                {{ t("me.sign_out") }}
              </v-btn>
            </div>
          </AppCard>
        </v-col>

        <v-col cols="12">
          <NotificationPreferencesCard class="mb-4" />
        </v-col>
      </v-row>

      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>
