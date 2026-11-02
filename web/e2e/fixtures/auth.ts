import { test as base, expect, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { loadStack, type StackInfo } from "./stack";
import { applyTheme, projectTheme } from "../helpers/screen";
import { totpNow, totpWindow } from "../helpers/totp";

const authDir = join(dirname(fileURLToPath(import.meta.url)), "..", ".auth");
const authStatePath = join(authDir, "owner.json");

type Fixtures = {
  stack: StackInfo;
  authedPage: Page;
};

const persistAuth = async (page: Page): Promise<void> => {
  mkdirSync(authDir, { recursive: true });
  writeFileSync(authStatePath, JSON.stringify(await page.context().storageState(), null, 2));
};

/** API sign-in; persists cookies so the next worker context picks them up. */
const apiOwnerSignIn = async (page: Page, stack: StackInfo): Promise<void> => {
  await page.context().clearCookies();
  const headers = {
    Origin: stack.baseUrl,
    "X-Requested-With": "EmailBridge",
    "Content-Type": "application/json",
  };
  const sign = await page.request.post(`${stack.baseUrl}/v1/auth/signin/credential`, {
    data: { credential: stack.owner.email, password: stack.owner.password },
    headers,
  });
  if (!sign.ok()) throw new Error(`api sign-in failed: ${sign.status()}`);
  const body = (await sign.json()) as { two_factor_required?: boolean };
  if (body.two_factor_required && stack.owner.totpSecret) {
    let last = "";
    for (let round = 0; round < 4; round++) {
      for (const code of totpWindow(stack.owner.totpSecret, 1)) {
        const verify = await page.request.post(`${stack.baseUrl}/v1/auth/two-factor/verify`, {
          data: { code, method: "totp" },
          headers,
        });
        if (verify.ok()) {
          await persistAuth(page);
          return;
        }
        last = await verify.text();
      }
      await new Promise((r) => setTimeout(r, 2000));
    }
    throw new Error(`api MFA failed: ${last}`);
  }
  await persistAuth(page);
};

/** Browser form sign-in — cookies are set the same way the SPA expects. */
const browserOwnerSignIn = async (page: Page, stack: StackInfo): Promise<void> => {
  await page.goto("/login");
  await page.locator('input[type="email"], input[autocomplete="username"]').first()
    .fill(stack.owner.email);
  await page.locator('input[type="password"]').first().fill(stack.owner.password);
  await page.getByTestId("login-submit").click();
  const mfa = page.getByTestId("mfa-code").locator("input").or(
    page.locator('input[autocomplete="one-time-code"]'),
  );
  await expect(mfa.first()).toBeVisible({ timeout: 20_000 });
  let ok = false;
  for (let round = 0; round < 4 && !ok; round++) {
    await mfa.first().fill(totpNow(stack.owner.totpSecret));
    await page.getByTestId("mfa-verify").click();
    try {
      await page.waitForURL((u) => !u.pathname.includes("/login"), { timeout: 8_000 });
      ok = true;
    } catch {
      await new Promise((r) => setTimeout(r, 2000));
    }
  }
  if (!ok) throw new Error("browser MFA did not leave /login");
  await persistAuth(page);
};

const ensureOwnerSession = async (page: Page, stack: StackInfo): Promise<void> => {
  const me = await page.request.get(`${stack.baseUrl}/v1/me`, {
    headers: { Origin: stack.baseUrl, "X-Requested-With": "EmailBridge" },
  });
  if (me.ok()) return;
  if (me.status() !== 401) {
    throw new Error(`owner /v1/me unexpected status ${me.status()}: ${await me.text()}`);
  }
  await apiOwnerSignIn(page, stack);
};

const landOnAdmin = async (page: Page, stack: StackInfo): Promise<void> => {
  for (let attempt = 0; attempt < 3; attempt++) {
    await page.goto("/");
    // Wait for either the admin shell or the login redirect (SPA boot).
    const shell = page.locator(".v-app-bar");
    const login = page.getByTestId("login-submit");
    await Promise.race([
      shell.first().waitFor({ state: "visible", timeout: 20_000 }).catch(() => undefined),
      login.waitFor({ state: "visible", timeout: 20_000 }).catch(() => undefined),
      page.waitForURL(/\/login/, { timeout: 20_000 }).catch(() => undefined),
    ]);
    if (await shell.first().isVisible().catch(() => false)) {
      await persistAuth(page);
      return;
    }
    // Prefer browser login so limen client state matches cookies.
    try {
      await browserOwnerSignIn(page, stack);
    } catch {
      await apiOwnerSignIn(page, stack);
    }
  }
  await page.goto("/");
  await expect(page.locator(".v-app-bar").first()).toBeVisible({ timeout: 30_000 });
  await persistAuth(page);
};

export const test = base.extend<Fixtures>({
  // eslint-disable-next-line no-empty-pattern -- Playwright fixture with no deps
  stack: async ({}, use) => {
    await use(loadStack());
  },

  storageState: authStatePath,

  authedPage: async ({ page, stack }, use, testInfo) => {
    const theme = projectTheme(testInfo.project.name);
    await ensureOwnerSession(page, stack);
    // Body is the settings patch map itself (`{ ui: … }`), not wrapped in `settings`.
    await page.request
      .patch(`${stack.baseUrl}/v1/me/settings`, {
        data: { ui: { theme, locale: "en" } },
        headers: {
          Origin: stack.baseUrl,
          "X-Requested-With": "EmailBridge",
          "Content-Type": "application/json",
        },
      })
      .catch(() => undefined);
    await landOnAdmin(page, stack);
    await applyTheme(page, theme);
    await use(page);
  },
});

export { expect };
