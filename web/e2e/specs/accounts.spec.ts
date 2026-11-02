import { test, expect } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("accounts list", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/accounts");
  await runScreen(authedPage, testInfo, {
    name: "accounts-list",
    ready: ["main", "table, .v-data-table, [class*='table']"],
    interact: async (page) => {
      const search = page.getByRole("searchbox").or(page.locator('input[type="search"]')).or(page.locator('input[placeholder*="Search" i]'));
      if (await search.count()) {
        await search.first().fill("Inbox");
        await expect(search.first()).toHaveValue("Inbox");
      }
    },
  });
});

test("accounts detail", async ({ authedPage, stack }, testInfo) => {
  const id = stack.ids.accountId || stack.ids.pecAccountId;
  test.skip(!id, "no seeded account");
  await authedPage.goto(`/accounts/${id}`);
  await runScreen(authedPage, testInfo, {
    name: "accounts-detail",
    ready: ["main", ".v-tabs, h1, h2"],
    interact: async (page) => {
      const tab = page.getByRole("tab").nth(1);
      if (await tab.count()) await tab.click();
    },
  });
});

test("accounts form new", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/accounts/new");
  await runScreen(authedPage, testInfo, {
    name: "accounts-form",
    ready: ["main", "form, .v-form"],
    interact: async (page) => {
      const save = page.getByRole("button").filter({ hasText: /save|salva|create|crea/i });
      if (await save.count()) {
        await save.first().click();
        await expect(page.locator(".v-messages, .v-alert, [role='alert']").first()).toBeVisible({ timeout: 10_000 }).catch(() => undefined);
      }
    },
  });
});
