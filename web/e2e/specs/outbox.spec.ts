import { test } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("outbox", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/outbox");
  await runScreen(authedPage, testInfo, {
    name: "outbox",
    ready: ["main"],
    // Do not open the org switcher / first combobox — that left an empty menu in shots.
  });
});
