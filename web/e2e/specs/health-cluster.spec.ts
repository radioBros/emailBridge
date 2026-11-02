import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("health", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/health/accounts");
  await runScreen(authedPage, testInfo, {
    name: "health",
    ready: ["main"],
  });
});

test("cluster", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/cluster");
  await runScreen(authedPage, testInfo, {
    name: "cluster",
    ready: ["main"],
  });
});
