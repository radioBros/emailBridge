import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("logs", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/logs");
  await runScreen(authedPage, testInfo, {
    name: "logs",
    ready: ["main"],
  });
});

test("audit", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/audit");
  await runScreen(authedPage, testInfo, {
    name: "audit",
    ready: ["main"],
  });
});
