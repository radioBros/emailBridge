import dns from "node:dns";
import { request as playwrightRequest, type APIRequestContext } from "@playwright/test";
import { totpNow, totpWindow } from "./totp";
import type { StackInfo } from "../fixtures/stack";

// uistack listens on 127.0.0.1; Node otherwise prefers ::1 for "localhost" on Windows.
dns.setDefaultResultOrder("ipv4first");

/** Sleep until the next 30s TOTP boundary (+pad). */
const waitNextTotpWindow = async (padMs = 500): Promise<void> => {
  const msIntoWindow = Date.now() % 30_000;
  const wait = 30_000 - msIntoWindow + padMs;
  await new Promise((r) => setTimeout(r, wait));
};

/**
 * Wait for a fresh TOTP window *before* sign-in. Never wait between credential
 * sign-in and MFA verify — the MFA challenge TTL is short, and sleeping after
 * sign-in was the cause of systematic `mfa_invalid` in global-setup.
 */
const ensureFreshTotpWindow = async (minRemainingMs = 10_000): Promise<void> => {
  const remaining = 30_000 - (Date.now() % 30_000);
  if (remaining < minRemainingMs) await waitNextTotpWindow();
};

export const signInOwner = async (
  stack: StackInfo,
  baseURL = stack.baseUrl,
): Promise<APIRequestContext> => {
  // Origin must stay as stack.baseUrl (localhost) — 127.0.0.1 Origin → origin_rejected.
  // Playwright's request client still resolves localhost to ::1 on Windows, so the
  // TCP baseURL is forced to 127.0.0.1 (uistack listens there only).
  const connectUrl = baseURL.replace("://localhost", "://127.0.0.1");
  const ctx = await playwrightRequest.newContext({ baseURL: connectUrl });
  const headers = {
    Origin: baseURL,
    "X-Requested-With": "EmailBridge",
    "Content-Type": "application/json",
  };

  let lastErr = "";

  for (let attempt = 0; attempt < 5; attempt++) {
    await ensureFreshTotpWindow();

    const sign = await ctx.post("/v1/auth/signin/credential", {
      data: { credential: stack.owner.email, password: stack.owner.password },
      headers,
    });
    if (!sign.ok()) {
      throw new Error(`sign-in failed: ${sign.status()} ${await sign.text()}`);
    }
    const body = (await sign.json()) as { two_factor_required?: boolean };
    if (!body.two_factor_required || !stack.owner.totpSecret) return ctx;

    // Verify immediately — do not sleep here (challenge TTL). Current code first (±1).
    let burned = false;
    for (const code of totpWindow(stack.owner.totpSecret, 1)) {
      const verify = await ctx.post("/v1/auth/two-factor/verify", {
        data: { code, method: "totp" },
        headers,
      });
      if (verify.ok()) return ctx;
      lastErr = await verify.text();
      if (lastErr.includes("already been used")) {
        burned = true;
        break;
      }
      if (
        !lastErr.includes("mfa_invalid") &&
        !lastErr.includes("Invalid code")
      ) {
        throw new Error(`MFA verify failed: ${verify.status()} ${lastErr}`);
      }
    }
    // Burned or invalid — wait for a brand-new window before the next sign-in.
    if (burned || attempt < 4) await waitNextTotpWindow();
  }
  throw new Error(
    `MFA verify failed after retries (last=${lastErr || "none"} code=${totpNow(stack.owner.totpSecret)})`,
  );
};
