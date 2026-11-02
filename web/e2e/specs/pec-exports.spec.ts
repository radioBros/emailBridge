import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("pec receipts + exports tabs", async ({ authedPage, stack }, testInfo) => {
  const id = stack.ids.pecAccountId || stack.ids.accountId;
  test.skip(!id, "no account for pec/exports");
  await authedPage.goto(`/accounts/${id}`);
  await runScreen(authedPage, testInfo, {
    name: "pec-exports",
    ready: [".v-tabs, [role='tablist']"],
    interact: async (page) => {
      const exportsTab = page.getByRole("tab").filter({ hasText: /export/i });
      if (await exportsTab.count()) await exportsTab.first().click();
      const pecTab = page.getByRole("tab").filter({ hasText: /pec/i });
      if (await pecTab.count()) await pecTab.first().click();
    },
  });
});
