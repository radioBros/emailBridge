<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiAlertCircle } from "@mdi/js";
import type { LoginErrorState } from "@/features/auth/composables/useLoginFlow";

const props = defineProps<{
  error: LoginErrorState | null;
}>();

const { t, te } = useI18n();

const message = computed(() => {
  const err = props.error;
  if (!err) return "";
  if (err.code === "account_locked") {
    if (err.retryAfterSec != null && err.retryAfterSec > 0) {
      return t("auth.error_account_locked", { sec: err.retryAfterSec });
    }
    return t("auth.error_account_locked_generic");
  }
  if (err.code === "validation_failed" && err.reason === "common_password") {
    return t("auth.error_common_password");
  }
  const key = `auth.error_${err.code}`;
  if (te(key)) return t(key);
  return err.detail || t("auth.error_generic");
});
</script>

<template>
  <v-alert
    v-if="error"
    :icon="mdiAlertCircle"
    type="error"
    class="mb-4"
    role="alert"
  >
    {{ message }}
  </v-alert>
</template>
