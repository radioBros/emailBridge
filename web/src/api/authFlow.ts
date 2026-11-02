import { auth } from "@/api/auth";
import type { ApiProblem } from "@/api/client";

/** Auth call that failed with an RFC 9457 problem (keeps Retry-After for lockout). */
export class AuthProblemError extends Error {
  readonly problem: ApiProblem;
  readonly retryAfterSec: number | null;

  constructor(problem: ApiProblem, retryAfterSec: number | null = null) {
    super(problem.detail || problem.title || problem.code);
    this.name = "AuthProblemError";
    this.problem = problem;
    this.retryAfterSec = retryAfterSec;
  }
}

const isProblemBody = (data: unknown): data is ApiProblem =>
  typeof data === "object" &&
  data !== null &&
  typeof (data as ApiProblem).code === "string" &&
  typeof (data as ApiProblem).status === "number";

const parseRetryAfter = (headers: Headers): number | null => {
  const raw = headers.get("Retry-After");
  if (!raw) return null;
  const sec = Number(raw);
  if (Number.isFinite(sec) && sec >= 0) return Math.ceil(sec);
  const when = Date.parse(raw);
  if (Number.isFinite(when)) return Math.max(0, Math.ceil((when - Date.now()) / 1000));
  return null;
};

type Json = Record<string, unknown>;

const authRequest = async <T = Json>(
  path: string,
  init: { method?: string; body?: unknown } = {},
): Promise<T> => {
  const response = await fetch(`/v1/auth${path}`, {
    method: init.method ?? (init.body !== undefined ? "POST" : "GET"),
    credentials: "include",
    headers: {
      Accept: "application/json",
      "Content-Type": "application/json",
      "X-Requested-With": "EmailBridge",
    },
    body: init.body !== undefined ? JSON.stringify(init.body) : undefined,
  });

  if (response.status === 204) return undefined as T;

  let data: unknown;
  const text = await response.text();
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }

  if (!response.ok) {
    if (isProblemBody(data)) {
      throw new AuthProblemError(data, parseRetryAfter(response.headers));
    }
    throw new AuthProblemError(
      {
        status: response.status,
        code:
          response.status === 401
            ? "invalid_credentials"
            : response.status === 423
              ? "account_locked"
              : "internal_error",
        detail: typeof data === "string" ? data : response.statusText,
      },
      parseRetryAfter(response.headers),
    );
  }

  return data as T;
};

export type SignInResult =
  | { kind: "authenticated" }
  | { kind: "two_factor_required" };

/** Password sign-in. Returns MFA challenge when the server sets `session_2fa`. */
export const signInWithPassword = async (
  email: string,
  password: string,
): Promise<SignInResult> => {
  const data = await authRequest<Json>("/signin/credential", {
    body: { credential: email.trim(), password, remember_me: true },
  });
  if (data?.two_factor_required === true || data?.twoFactorRequired === true) {
    return { kind: "two_factor_required" };
  }
  await auth.getSession();
  return { kind: "authenticated" };
};

export type MfaMethod = "totp" | "otp" | "backup";

/** Complete the second factor after `two_factor_required`. */
export const verifyMfa = async (code: string, method: MfaMethod): Promise<void> => {
  const body: Json = { code: code.trim() };
  if (method === "totp") body.method = "totp";
  if (method === "otp") body.method = "otp";
  // backup codes: omit method (S10 §2.2 / integration tests)
  await authRequest("/two-factor/verify", { body });
  await auth.getSession();
};

/** Send an email OTP while a `session_2fa` challenge cookie is present. */
export const sendMfaOtp = async (email: string): Promise<void> => {
  await authRequest("/two-factor/otp/send", { body: { email: email.trim() } });
};

export const signInWithPasskey = async (): Promise<void> => {
  await auth.signIn.passkey();
};

export type TotpSetup = { uri: string; secret: string };

export const initiateTotpSetup = async (password: string): Promise<TotpSetup> => {
  const data = await authRequest<Json>("/two-factor/initiate-setup", {
    body: { password },
  });
  const uri = String(data.uri ?? "");
  let secret = typeof data.secret === "string" ? data.secret : "";
  if (!secret && uri) {
    try {
      secret = new URL(uri).searchParams.get("secret") ?? "";
    } catch {
      secret = "";
    }
  }
  return { uri, secret };
};

export const finalizeTotpSetup = async (code: string): Promise<string[]> => {
  await authRequest("/two-factor/finalize-setup", { body: { code: code.trim() } });
  await auth.getSession();
  return fetchBackupCodes();
};

export const fetchBackupCodes = async (): Promise<string[]> => {
  const data = await authRequest<unknown>("/two-factor/backup-codes", { method: "GET" });
  if (Array.isArray(data)) return data.map(String);
  if (data && typeof data === "object" && Array.isArray((data as Json).backup_codes)) {
    return ((data as Json).backup_codes as unknown[]).map(String);
  }
  if (data && typeof data === "object" && Array.isArray((data as Json).backupCodes)) {
    return ((data as Json).backupCodes as unknown[]).map(String);
  }
  return [];
};

export const disableTotp = async (password: string): Promise<void> => {
  await authRequest("/two-factor/disable", { body: { password } });
  await auth.getSession();
};
