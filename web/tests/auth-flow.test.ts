import { afterEach, describe, expect, it, vi } from "vitest";
import {
  AuthProblemError,
  sendMfaOtp,
  signInWithPassword,
  verifyMfa,
} from "@/api/authFlow";

vi.mock("@/api/auth", () => ({
  auth: {
    getSession: vi.fn().mockResolvedValue(null),
  },
}));

const jsonResponse = (status: number, body: unknown, headers?: Record<string, string>) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });

describe("authFlow", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.clearAllMocks();
  });

  it("returns two_factor_required from sign-in", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse(200, { two_factor_required: true })),
    );
    await expect(signInWithPassword("a@b.c", "password-long-enough")).resolves.toEqual({
      kind: "two_factor_required",
    });
  });

  it("throws AuthProblemError with Retry-After on lockout", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse(423, { status: 423, code: "account_locked", detail: "locked" }, {
          "Retry-After": "15",
        }),
      ),
    );
    try {
      await signInWithPassword("a@b.c", "x");
      expect.unreachable();
    } catch (err) {
      expect(err).toBeInstanceOf(AuthProblemError);
      expect((err as AuthProblemError).problem.code).toBe("account_locked");
      expect((err as AuthProblemError).retryAfterSec).toBe(15);
    }
  });

  it("verifies MFA and sends OTP", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(jsonResponse(200, {}))
      .mockResolvedValueOnce(jsonResponse(200, { message: "sent" }));
    vi.stubGlobal("fetch", fetchMock);
    await verifyMfa("123456", "totp");
    await sendMfaOtp("a@b.c");
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(String(fetchMock.mock.calls[0][0])).toContain("/two-factor/verify");
    expect(String(fetchMock.mock.calls[1][0])).toContain("/two-factor/otp/send");
  });
});
