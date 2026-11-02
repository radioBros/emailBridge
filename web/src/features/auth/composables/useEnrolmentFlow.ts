import { computed, ref } from "vue";
import {
  AuthProblemError,
  finalizeTotpSetup,
  initiateTotpSetup,
  type TotpSetup,
} from "@/api/authFlow";
import { auth } from "@/api/auth";
import { problemOf } from "@/api/problems";

export type EnrolmentChoice = "choose" | "passkey" | "totp" | "backup_codes" | "done";

/** MFA enrolment state machine (S10 §2.2 enrolment). */
export const useEnrolmentFlow = () => {
  const phase = ref<EnrolmentChoice>("choose");
  const password = ref("");
  const totpCode = ref("");
  const passkeyName = ref("");
  const setup = ref<TotpSetup | null>(null);
  const backupCodes = ref<string[]>([]);
  const loading = ref(false);
  const errorCode = ref<string | null>(null);
  const errorDetail = ref<string | null>(null);

  const clearError = (): void => {
    errorCode.value = null;
    errorDetail.value = null;
  };

  const setError = (err: unknown): void => {
    if (err instanceof AuthProblemError) {
      errorCode.value = err.problem.code;
      errorDetail.value = err.problem.detail ?? null;
      return;
    }
    const p = problemOf(err);
    errorCode.value = p?.code ?? "internal_error";
    errorDetail.value = p?.detail ?? null;
  };

  const choosePasskey = (): void => {
    clearError();
    phase.value = "passkey";
  };

  const chooseTotp = (): void => {
    clearError();
    phase.value = "totp";
  };

  const startTotp = async (): Promise<boolean> => {
    clearError();
    loading.value = true;
    try {
      setup.value = await initiateTotpSetup(password.value);
      return true;
    } catch (err) {
      setError(err);
      return false;
    } finally {
      loading.value = false;
    }
  };

  const confirmTotp = async (): Promise<boolean> => {
    clearError();
    loading.value = true;
    try {
      backupCodes.value = await finalizeTotpSetup(totpCode.value);
      phase.value = "backup_codes";
      return true;
    } catch (err) {
      setError(err);
      return false;
    } finally {
      loading.value = false;
    }
  };

  const registerPasskey = async (): Promise<boolean> => {
    clearError();
    loading.value = true;
    try {
      await auth.passkey.register({
        name: passkeyName.value.trim() || undefined,
      });
      phase.value = "done";
      return true;
    } catch (err) {
      setError(err);
      return false;
    } finally {
      loading.value = false;
    }
  };

  const finishBackupCodes = (): void => {
    phase.value = "done";
  };

  const hasSetup = computed(() => Boolean(setup.value?.uri));

  return {
    phase,
    password,
    totpCode,
    passkeyName,
    setup,
    backupCodes,
    loading,
    errorCode,
    errorDetail,
    hasSetup,
    clearError,
    choosePasskey,
    chooseTotp,
    startTotp,
    confirmTotp,
    registerPasskey,
    finishBackupCodes,
  };
};
