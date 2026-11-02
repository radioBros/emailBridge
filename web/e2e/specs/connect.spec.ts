import { test as base } from "@playwright/test";
import { loadStack } from "../fixtures/stack";
import { runScreen, applyTheme, projectTheme } from "../helpers/screen";

const test = base;
test.use({ storageState: { cookies: [], origins: [] } });

test("public connect page", async ({ page }, testInfo) => {
  const stack = loadStack();
  const token = stack.ids.connectToken || "demo-token";
  await page.goto(`/connect/${encodeURIComponent(token)}`, {
    waitUntil: "domcontentloaded",
  });
  await applyTheme(page, projectTheme(testInfo.project.name));
  await runScreen(page, testInfo, {
    name: "connect",
    // Hosted seed must bootstrap the form (not the invalid-link alert).
    ready: [".v-application", "form, input[type='email']"],
    allowFailedURLs: [".png", ".svg", ".webp", ".ico", "favicon"],
  });
});
