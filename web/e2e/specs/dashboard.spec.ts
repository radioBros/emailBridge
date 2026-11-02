import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("dashboard", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/");
  await runScreen(authedPage, testInfo, {
    name: "dashboard",
    ready: ["main", ".v-container"],
    interact: async (page) => {
      const refresh = page.getByRole("button").filter({ hasText: /refresh|aggiorna/i });
      if (await refresh.count()) await refresh.first().click();
    },
  });
});
