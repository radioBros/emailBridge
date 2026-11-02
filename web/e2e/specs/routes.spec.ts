import { test } from "../fixtures/auth";
import { runScreen, waitForDialogSettled } from "../helpers/screen";

test("routes", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/routes");
  await runScreen(authedPage, testInfo, {
    name: "routes",
    ready: ["main"],
    interact: async (page) => {
      const create = page.getByTestId("routes-create");
      if (await create.count()) {
        await create.click();
        await waitForDialogSettled(page);
      }
    },
  });
});
