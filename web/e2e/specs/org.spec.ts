import { test, expect } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("members", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/members");
  await runScreen(authedPage, testInfo, {
    name: "members",
    ready: ["main"],
    interact: async (page) => {
      const invite = page.getByRole("button").filter({ hasText: /invite|invita/i });
      if (await invite.count()) {
        await invite.first().click();
        await expect(page.locator(".v-dialog, [role='dialog']").first()).toBeVisible({ timeout: 10_000 }).catch(() => undefined);
        await page.keyboard.press("Escape");
      }
    },
  });
});

test("api keys", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/api-keys");
  await runScreen(authedPage, testInfo, {
    name: "api-keys",
    ready: ["main"],
  });
});
