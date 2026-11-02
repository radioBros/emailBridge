import { test, expect } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("organizations", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/organizations");
  await runScreen(authedPage, testInfo, {
    name: "organizations",
    ready: ["main"],
    interact: async (page) => {
      const create = page.getByRole("button").filter({
        hasText: /create organisation|crea organizzazione|crear organización|créer|organisation erstellen|criar|создать|创建组织|組織を作成/i,
      });
      if (await create.count()) {
        await create.first().click();
        await expect(page.locator(".v-dialog, [role='dialog']").first())
          .toBeVisible({ timeout: 10_000 })
          .catch(() => undefined);
        await page.keyboard.press("Escape");
      }
    },
  });
});
