import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("sandbox", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/sandbox");
  await runScreen(authedPage, testInfo, {
    name: "sandbox",
    ready: ["main"],
  });
});
