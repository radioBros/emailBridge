import { beforeEach, describe, expect, it, vi } from "vitest";
import { useLoginFlow } from "@/features/auth/composables/useLoginFlow";
import { AuthProblemError } from "@/api/authFlow";

vi.mock("@/api/authFlow", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/authFlow")>();
  return {
    ...actual,
    signInWithPassword: vi.fn(),
    verifyMfa: vi.fn(),
    sendMfaOtp: vi.fn(),
    signInWithPasskey: vi.fn(),
  };
});

import {
  sendMfaOtp,
  signInWithPasskey,
  signInWithPassword,
  verifyMfa,
} from "@/api/authFlow";

describe("useLoginFlow", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("moves to MFA when the server requires a second factor", async () => {
    vi.mocked(signInWithPassword).mockResolvedValue({ kind: "two_factor_required" });
    const flow = useLoginFlow();
    flow.email.value = "ada@example.test";
    flow.password.value = "correct-horse-battery-staple";
    const outcome = await flow.submitCredentials();
    expect(outcome).toBe("mfa");
    expect(flow.step.value).toBe("mfa");
  });

  it("returns ok after a full password sign-in", async () => {
    vi.mocked(signInWithPassword).mockResolvedValue({ kind: "authenticated" });
    const flow = useLoginFlow();
    flow.email.value = "ada@example.test";
    flow.password.value = "correct-horse-battery-staple";
    expect(await flow.submitCredentials()).toBe("ok");
  });

  it("surfaces invalid_credentials on the credentials step", async () => {
    vi.mocked(signInWithPassword).mockRejectedValue(
      new AuthProblemError({ status: 401, code: "invalid_credentials", detail: "nope" }),
    );
    const flow = useLoginFlow();
    flow.email.value = "ada@example.test";
    flow.password.value = "wrong";
    expect(await flow.submitCredentials()).toBe("error");
    expect(flow.error.value?.code).toBe("invalid_credentials");
    expect(flow.step.value).toBe("credentials");
  });

  it("surfaces account_locked with retry-after", async () => {
    vi.mocked(signInWithPassword).mockRejectedValue(
      new AuthProblemError(
        { status: 423, code: "account_locked", detail: "locked" },
        42,
      ),
    );
    const flow = useLoginFlow();
    flow.email.value = "ada@example.test";
    flow.password.value = "x";
    await flow.submitCredentials();
    expect(flow.error.value).toEqual({
      code: "account_locked",
      detail: "locked",
      retryAfterSec: 42,
      reason: undefined,
    });
  });

  it("verifies MFA and switches tabs", async () => {
    vi.mocked(verifyMfa).mockResolvedValue(undefined);
    const flow = useLoginFlow();
    flow.enterMfa();
    flow.mfaTab.value = "otp";
    flow.mfaCode.value = "123456";
    expect(await flow.submitMfa()).toBe("ok");
    expect(verifyMfa).toHaveBeenCalledWith("123456", "otp");
  });

  it("renders MFA invalid errors", async () => {
    vi.mocked(verifyMfa).mockRejectedValue(
      new AuthProblemError({ status: 401, code: "mfa_invalid", detail: "bad" }),
    );
    const flow = useLoginFlow();
    flow.enterMfa();
    flow.mfaCode.value = "000000";
    expect(await flow.submitMfa()).toBe("error");
    expect(flow.error.value?.code).toBe("mfa_invalid");
  });

  it("sends email OTP and starts cooldown", async () => {
    vi.useFakeTimers();
    vi.mocked(sendMfaOtp).mockResolvedValue(undefined);
    const flow = useLoginFlow();
    flow.email.value = "ada@example.test";
    expect(await flow.sendOtp()).toBe(true);
    expect(flow.otpCooldownSec.value).toBe(60);
    await vi.advanceTimersByTimeAsync(1000);
    expect(flow.otpCooldownSec.value).toBe(59);
    vi.useRealTimers();
  });

  it("handles passkey success", async () => {
    vi.mocked(signInWithPasskey).mockResolvedValue(undefined);
    const flow = useLoginFlow();
    expect(await flow.submitPasskey()).toBe("ok");
  });
});
