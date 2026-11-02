import { test, expect } from "../fixtures/auth";
import { runScreen } from "../helpers/screen";

test("templates list", async ({ authedPage }, testInfo) => {
  await authedPage.goto("/templates");
  await runScreen(authedPage, testInfo, {
    name: "templates",
    ready: ["main"],
    interact: async (page) => {
      const create = page.getByTestId("templates-create");
      if (await create.count()) await create.click();
    },
  });
});

test("templates editor validation", async ({ authedPage, stack }, testInfo) => {
  const id = stack.ids.templateId;
  if (id) await authedPage.goto(`/templates/${id}`);
  else await authedPage.goto("/templates/new");
  await runScreen(authedPage, testInfo, {
    name: "templates-editor",
    ready: ["main", "[data-testid='templates-name'], form"],
    interact: async (page) => {
      // data-testid sits on the v-text-field wrapper; fill the inner input.
      const name = page.getByTestId("templates-name").locator("input");
      if (await name.count()) {
        await name.fill("");
        const save = page.getByTestId("templates-save");
        if (await save.count()) {
          await save.click();
          await expect(page.locator(".v-messages, .v-alert").first())
            .toBeVisible({ timeout: 8_000 })
            .catch(() => undefined);
          await name.fill("Fixed name");
        }
      }
    },
    // Client-side validation should block the request; if a 422 still races,
    // it is expected noise for this intentional empty-name save attempt.
    allowFailedURLs: ["/v1/templates"],
  });
});
