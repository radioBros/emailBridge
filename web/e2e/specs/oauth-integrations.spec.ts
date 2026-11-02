import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("oauth apps", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/oauth-apps");
  await runScreen(authedPage, testInfo, {
    name: "oauth-apps",
    ready: ["main"],
  });
});

test("integrations", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/integrations");
  await runScreen(authedPage, testInfo, {
    name: "integrations",
    ready: ["main"],
  });
});
