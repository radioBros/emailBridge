import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("events", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/events");
  await runScreen(authedPage, testInfo, {
    name: "events",
    ready: ["main"],
    interact: async (page) => {
      const row = page.locator("table tbody tr").first();
      if (await row.count()) {
        await row.scrollIntoViewIfNeeded().catch(() => undefined);
        await row.click({ timeout: 5_000 }).catch(() => undefined);
        await page
          .locator(".v-navigation-drawer, [role='dialog']")
          .first()
          .waitFor({ state: "visible", timeout: 10_000 })
          .catch(() => undefined);
      }
    },
  });
});
