import { test, expect } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("suppression lists", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/suppression");
  await runScreen(authedPage, testInfo, {
    name: "suppression",
    ready: ["main"],
    interact: async (page) => {
      const create = page.getByTestId("suppression-create");
      if (await create.count()) {
        await create.click();
        await expect(page.locator(".v-dialog, [role='dialog']").first()).toBeVisible({ timeout: 10_000 });
        await page.keyboard.press("Escape");
      }
    },
  });
});

test("suppression entries", async ({ authedPage, stack }, testInfo) => {
  const id = stack.ids.suppressionListId;
  test.skip(!id, "no suppression list");
  await authedPage.goto(`/suppression/${id}`);
  await runScreen(authedPage, testInfo, {
    name: "suppression-entries",
    ready: ["main"],
  });
});
