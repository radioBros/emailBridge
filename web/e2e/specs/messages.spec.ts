import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("messages browser", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/messages");
  await runScreen(authedPage, testInfo, {
    name: "messages",
    ready: ["main"],
    interact: async (page) => {
      // Phone starts on the folder pane; nav drawer list-items are off-screen and
      // must not be the click target (they timed out as "outside viewport").
      const folder = page.locator(".messages-folder-tree .v-list-item").first();
      if (await folder.count()) {
        await folder.click();
      }

      const row = page
        .locator("[data-testid^='card-'], [data-testid^='row-'], table tbody tr")
        .first();
      if (await row.count()) {
        await row.click();
        await page
          .locator(".v-navigation-drawer, [class*='reader'], aside")
          .first()
          .waitFor({ state: "visible", timeout: 15_000 })
          .catch(() => undefined);
      }
    },
  });
});
