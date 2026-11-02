import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("me profile sessions", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/me");
  await runScreen(authedPage, testInfo, {
    name: "me",
    ready: ["main"],
    interact: async (page) => {
      const theme = page.getByRole("combobox").filter({ hasText: /theme|tema/i }).or(page.locator('label:has-text("Theme")'));
      if (await theme.count()) await theme.first().click().catch(() => undefined);
    },
  });
});
