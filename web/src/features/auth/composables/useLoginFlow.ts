import { computed, ref } from "vue";
import {
  AuthProblemError,
  sendMfaOtp,
  signInWithPasskey,
  signInWithPassword,
  verifyMfa,
  type MfaMethod,
} from "@/api/authFlow";
import { problemOf } from "@/api/problems";

export type LoginStep = "credentials" | "mfa";
export type MfaTab = "totp" | "otp" | "backup";

export interface LoginErrorState {
  code: string;
  detail?: string;
  retryAfterSec: number | null;
  reason?: string;
}

const OTP_COOLDOWN_SEC = 60;

/** Login + MFA step machine (S10 §2.2, S13 §4.1 Login). */
export const useLoginFlow = () => {
  const step = ref<LoginStep>("credentials");
  const email = ref("");
  const password = ref("");
  const mfaCode = ref("");
  const mfaTab = ref<MfaTab>("totp");
  const loading = ref(false);
  const passkeyLoading = ref(false);
  const otpSending = ref(false);
  const otpCooldownSec = ref(0);
  const error = ref<LoginErrorState | null>(null);

  let cooldownTimer: ReturnType<typeof setInterval> | null = null;

  const clearError = (): void => {
    error.value = null;
  };

  const setErrorFrom = (err: unknown): void => {
    if (err instanceof AuthProblemError) {
      const reason =
        err.problem.code === "validation_failed"
          ? String(
              (err.problem.errors?.[0] as { code?: string } | undefined)?.code ??
                (err.problem as { reason?: string }).reason ??
                "",
            )
          : undefined;
      error.value = {
        code: err.problem.code,
        detail: err.problem.detail,
        retryAfterSec: err.retryAfterSec,
        reason: reason || undefined,
      };
      return;
    }
    const p = problemOf(err);
    error.value = {
      code: p?.code ?? "internal_error",
      detail: p?.detail,
      retryAfterSec: null,
    };
  };

  const startOtpCooldown = (sec = OTP_COOLDOWN_SEC): void => {
    otpCooldownSec.value = sec;
    if (cooldownTimer) clearInterval(cooldownTimer);
    cooldownTimer = setInterval(() => {
      otpCooldownSec.value -= 1;
      if (otpCooldownSec.value <= 0 && cooldownTimer) {
        clearInterval(cooldownTimer);
        cooldownTimer = null;
        otpCooldownSec.value = 0;
      }
    }, 1000);
  };

  const submitCredentials = async (): Promise<"ok" | "mfa" | "error"> => {
    clearError();
    loading.value = true;
    try {
      const result = await signInWithPassword(email.value, password.value);
      if (result.kind === "two_factor_required") {
        step.value = "mfa";
        mfaCode.value = "";
        return "mfa";
      }
      return "ok";
    } catch (err) {
      setErrorFrom(err);
      return "error";
    } finally {
      loading.value = false;
    }
  };

  const submitMfa = async (): Promise<"ok" | "error"> => {
    clearError();
    loading.value = true;
    try {
      const method: MfaMethod =
        mfaTab.value === "totp" ? "totp" : mfaTab.value === "otp" ? "otp" : "backup";
      await verifyMfa(mfaCode.value, method);
      return "ok";
    } catch (err) {
      setErrorFrom(err);
      return "error";
    } finally {
      loading.value = false;
    }
  };

  const sendOtp = async (): Promise<boolean> => {
    clearError();
    if (otpCooldownSec.value > 0) return false;
    otpSending.value = true;
    try {
      await sendMfaOtp(email.value);
      startOtpCooldown();
      return true;
    } catch (err) {
      setErrorFrom(err);
      if (err instanceof AuthProblemError && err.retryAfterSec) {
        startOtpCooldown(err.retryAfterSec);
      }
      return false;
    } finally {
      otpSending.value = false;
    }
  };

  const submitPasskey = async (): Promise<"ok" | "error"> => {
    clearError();
    passkeyLoading.value = true;
    try {
      await signInWithPasskey();
      return "ok";
    } catch (err) {
      setErrorFrom(err);
      return "error";
    } finally {
      passkeyLoading.value = false;
    }
  };

  const backToCredentials = (): void => {
    step.value = "credentials";
    mfaCode.value = "";
    clearError();
  };

  const enterMfa = (): void => {
    step.value = "mfa";
    clearError();
  };

  const canSubmitCredentials = computed(
    () => email.value.trim().length > 0 && password.value.length > 0 && !loading.value,
  );

  const canSubmitMfa = computed(() => mfaCode.value.trim().length > 0 && !loading.value);

  return {
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
    clearError,
    submitCredentials,
    submitMfa,
    sendOtp,
    submitPasskey,
    backToCredentials,
    enterMfa,
  };
};
