import { test } from "../fixtures/auth";
import { runScreen, waitForDialogSettled } from "../helpers/screen";

test("webhooks", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/webhooks");
  await runScreen(authedPage, testInfo, {
    name: "webhooks",
    ready: ["main"],
    interact: async (page) => {
      const create = page.getByTestId("webhooks-create");
      if (await create.count()) {
        await create.click();
        await waitForDialogSettled(page);
      }
    },
  });
});

test("deliveries", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/deliveries");
  await runScreen(authedPage, testInfo, {
    name: "deliveries",
    ready: ["main"],
    interact: async (page) => {
      const row = page.locator("table tbody tr").first();
      if (await row.count()) await row.click().catch(() => undefined);
    },
  });
});
