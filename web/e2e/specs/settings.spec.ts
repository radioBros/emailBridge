import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("settings + switches + oidc + ai/mcp", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/settings");
  await runScreen(authedPage, testInfo, {
    name: "settings",
    // Desktop: v-tabs; phone: select (tabs truncate / zero-height on narrow).
    ready: [".v-tabs, [role='tablist'], [data-testid='settings-tab-select']"],
    interact: async (page) => {
      const select = page.getByTestId("settings-tab-select");
      if (await select.count()) {
        for (let i = 0; i < 4; i++) {
          await select.click();
          const opts = page.locator(
            ".v-overlay--active [role='option'], .v-overlay--active .v-list-item",
          );
          await opts.nth(i).waitFor({ state: "visible", timeout: 5_000 }).catch(() => undefined);
          if (await opts.nth(i).count()) await opts.nth(i).click();
          else {
            await page.keyboard.press("Escape");
            break;
          }
        }
        return;
      }
      const tabs = page.getByRole("tab");
      const n = await tabs.count();
      for (let i = 0; i < Math.min(n, 4); i++) {
        await tabs.nth(i).click();
        await tabs.nth(i).getAttribute("aria-selected");
      }
    },
  });
});
