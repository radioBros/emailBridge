import { test as base, expect } from "@playwright/test";
import { loadStack } from "../fixtures/stack";
import { totpNow } from "../helpers/totp";
import { runScreen, applyTheme, projectTheme } from "../helpers/screen";

const test = base;
test.use({ storageState: { cookies: [], origins: [] } });

test("login + MFA", async ({ page }, testInfo) => {
  const stack = loadStack();
  const theme = projectTheme(testInfo.project.name);
  await page.goto("/login");
  await applyTheme(page, theme);

  await runScreen(page, testInfo, {
    name: "login",
    ready: ["form", "input"],
    interact: async (p) => {
      // Target the password submit via test id — the passkey button also
      // matches /sign in/i and was eating the click (login never reached MFA).
      const submit = p.getByTestId("login-submit");

      // Use viewer — not owner. Completing owner MFA can race the shared
      // storageState session used by every other authed spec.
      await p.locator('input[type="email"], input[name="email"], input[autocomplete="username"]').first().fill(stack.viewer.email);
      await p.locator('input[type="password"]').first().fill(stack.viewer.password);
      await submit.click();

      // MFA step — stable landmarks (data-testid on the TOTP field).
      const mfa = p.getByTestId("mfa-code").locator("input").or(
        p.locator('input[autocomplete="one-time-code"]'),
      );
      await expect(mfa.first()).toBeVisible({ timeout: 20_000 });
      await mfa.first().fill(totpNow(stack.viewer.totpSecret));
      const verify = p.getByTestId("mfa-verify").or(
        p.getByRole("button").filter({ hasText: /verify|verifica|confirm|conferma/i }),
      );
      if (await verify.count()) await verify.first().click();
      await p.waitForURL((u) => !u.pathname.includes("/login"), { timeout: 30_000 }).catch(() => undefined);
    },
    allowFailedURLs: ["/v1/auth/", "401", "Unauthorized"],
  });
});

test("accept invite page", async ({ page }, testInfo) => {
  const stack = loadStack();
  const token = stack.ids.inviteToken || "missing";
  await page.goto(`/accept-invite?token=${encodeURIComponent(token)}`);
  await applyTheme(page, projectTheme(testInfo.project.name));
  await runScreen(page, testInfo, {
    name: "accept-invite",
    ready: ["main, form, .v-card"],
    allowFailedURLs: ["/v1/invitations/"],
  });
});
