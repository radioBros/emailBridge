import { test, expect } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("gateways", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/gateways");
  await runScreen(authedPage, testInfo, {
    name: "gateways",
    ready: ["main"],
    interact: async (page) => {
      const create = page.getByTestId("gateways-create");
      if (await create.count()) {
        await create.click();
        await expect(page.locator(".v-dialog, [role='dialog']").first()).toBeVisible({ timeout: 10_000 });
        await page.keyboard.press("Escape");
      }
    },
  });
});
