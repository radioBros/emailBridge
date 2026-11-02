<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiAccountPlus, mdiLockOutline } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import {
  acceptInvite,
  inviteErrorCode,
  isExpiredInviteCode,
  lookupInvite,
  takeInviteTokenFromHash,
  type InvitePreview,
} from "@/api/invitations";
import {
  passwordStrengthColor,
  passwordStrengthOf,
} from "@/features/auth/composables/passwordStrength";
import { useSessionStore } from "@/stores/session";
import { combine, password as passwordRules, required } from "@/constants/Validations";
import { actionOf } from "@/constants/Actions";
import { useLoadState } from "@/composables/useLoadState";

const INVITE_TOKEN_KEY = "eb_invite_token";

const { t } = useI18n();
const router = useRouter();
const session = useSessionStore();
const { isFirstLoad, start, finish } = useLoadState();

const token = ref("");
const preview = ref<InvitePreview | null>(null);
const password = ref("");
const displayName = ref("");
const loading = ref(false);
const errorKey = ref<string | null>(null);
const formRef = ref<{ validate: () => Promise<{ valid: boolean }> } | null>(null);
const createAction = actionOf("create");

const strength = computed(() => passwordStrengthOf(password.value));
const strengthColor = computed(() => passwordStrengthColor(strength.value));
const strengthLabel = computed(() => t(`auth.invite_strength_${strength.value}`));
const needsPassword = computed(() => Boolean(preview.value && !preview.value.existingUser));
const needsSignIn = computed(
  () => Boolean(preview.value?.existingUser && !session.isAuthenticated),
);

const resolveToken = (): string => {
  const fromHash = takeInviteTokenFromHash();
  if (fromHash) {
    sessionStorage.setItem(INVITE_TOKEN_KEY, fromHash);
    return fromHash;
  }
  return sessionStorage.getItem(INVITE_TOKEN_KEY) ?? "";
};

const load = async (): Promise<void> => {
  start();
  errorKey.value = null;
  token.value = resolveToken();
  if (!token.value) {
    errorKey.value = "auth.invite_invalid";
    finish(false);
    return;
  }
  try {
    preview.value = await lookupInvite(token.value);
    finish(true);
  } catch (err) {
    const code = inviteErrorCode(err);
    errorKey.value = isExpiredInviteCode(code) ? "auth.invite_expired" : "auth.invite_invalid";
    finish(false);
  }
};

onMounted(() => {
  void load();
});

const goSignIn = async (): Promise<void> => {
  if (token.value) sessionStorage.setItem(INVITE_TOKEN_KEY, token.value);
  await router.push({
    name: "login",
    query: { redirect: "/accept-invite" },
  });
};

const onAccept = async (): Promise<void> => {
  if (needsSignIn.value) {
    await goSignIn();
    return;
  }
  if (needsPassword.value) {
    const result = await formRef.value?.validate();
    if (result && !result.valid) return;
  }
  loading.value = true;
  errorKey.value = null;
  try {
    const tok = token.value || sessionStorage.getItem(INVITE_TOKEN_KEY) || "";
    if (!tok) {
      errorKey.value = "auth.invite_invalid";
      return;
    }
    await acceptInvite({
      token: tok,
      password: needsPassword.value ? password.value : undefined,
      name: displayName.value.trim() || undefined,
    });
    sessionStorage.removeItem(INVITE_TOKEN_KEY);
    await session.loadMe();
    session.needsEnrolment = true;
    await router.replace({ name: "enrolment" });
  } catch (err) {
    const code = inviteErrorCode(err);
    if (code === "validation_failed") errorKey.value = "auth.error_common_password";
    else if (isExpiredInviteCode(code)) errorKey.value = "auth.invite_expired";
    else errorKey.value = "auth.invite_invalid";
  } finally {
    loading.value = false;
  }
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
            :icon="mdiAccountPlus"
          />
        </v-avatar-lead>
        <div class="min-w-0">
          <div class="text-title-large font-weight-bold truncate-1">
            {{
              preview
                ? t("auth.invite_title", { org: preview.organizationName })
                : t("auth.brand")
            }}
          </div>
          <div class="text-body-small text-medium-emphasis">
            <template v-if="preview?.inviterName">
              {{ t("auth.invite_subtitle", { inviter: preview.inviterName }) }}
            </template>
            <template v-else>
              {{ t("auth.invite_subtitle_no_inviter") }}
            </template>
          </div>
        </div>
      </div>
    </template>

    <LoadingSkeleton
      v-if="isFirstLoad"
      shape="form"
    />

    <template v-else>
      <v-alert
        v-if="errorKey && !preview"
        type="error"
        class="mb-0"
        role="alert"
      >
        {{ t(errorKey) }}
      </v-alert>

      <template v-else-if="preview">
        <v-alert
          v-if="errorKey"
          type="error"
          class="mb-4"
          role="alert"
        >
          {{ t(errorKey) }}
        </v-alert>

        <v-text-field
          :model-value="preview.email"
          :label="t('auth.invite_email')"
          class="mb-2 no-validation"
          readonly
        />

        <template v-if="needsSignIn">
          <p class="text-body-medium mb-4">
            {{ t("auth.invite_existing_body") }}
          </p>
          <v-btn
            :color="createAction.color"
            block
            @click="goSignIn"
          >
            {{ t("auth.invite_sign_in") }}
          </v-btn>
        </template>

        <v-form
          v-else
          ref="formRef"
          @submit.prevent="onAccept"
        >
          <v-text-field
            v-if="needsPassword"
            v-model="displayName"
            :label="t('auth.invite_name')"
            class="mb-2 no-validation"
            name="name"
            autocomplete="name"
          />
          <template v-if="needsPassword">
            <v-text-field
              v-model="password"
              :label="t('auth.invite_password')"
              :hint="t('auth.invite_password_hint')"
              :rules="combine(required, passwordRules)"
              :prepend-inner-icon="mdiLockOutline"
              :color="strengthColor"
              type="password"
              name="new-password"
              autocomplete="new-password"
              class="mb-1"
              autofocus
              persistent-hint
            />
            <div
              :class="`text-${strengthColor}`"
              class="text-label-large mb-4"
            >
              {{ strengthLabel }}
            </div>
          </template>
          <v-btn
            :loading="loading"
            :disabled="needsPassword && (strength === 'empty' || strength === 'too_short')"
            :color="createAction.color"
            type="submit"
            block
          >
            {{ needsPassword ? t("auth.invite_accept") : t("auth.invite_join") }}
          </v-btn>
        </v-form>
      </template>
    </template>
  </AppCard>
</template>
