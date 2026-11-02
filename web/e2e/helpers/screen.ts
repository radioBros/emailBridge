import { expect, type Page, type TestInfo } from "@playwright/test";
import AxeBuilder from "@axe-core/playwright";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

export type ScreenCheckOptions = {
  /** Path segment under e2e/screens/<project>/ */
  name: string;
  /** CSS selectors that must become visible (data ready). */
  ready: string | string[];
  /** Optional interaction after ready. */
  interact?: (page: Page) => Promise<void>;
  /** Request URL substrings that may fail deliberately. */
  allowFailedURLs?: string[];
};

export const projectTheme = (projectName: string): "light" | "dark" =>
  projectName.includes("dark") ? "dark" : "light";

export const applyTheme = async (
  page: Page,
  theme: "light" | "dark",
): Promise<void> => {
  await page.emulateMedia({ colorScheme: theme });
  // Persist preference so AdminLayout boot / useThemePreference keep the project theme
  // (never leave "system" fighting the project, and never rely on a class-only toggle).
  await page.evaluate((t) => {
    try {
      localStorage.setItem("eb.ui.theme", t);
    } catch {
      /* ignore */
    }
    const app = document.querySelector(".v-application");
    if (!app) return;
    try {
      const vueApp = (
        document.querySelector("#app") as unknown as {
          __vue_app__?: {
            config?: {
              globalProperties?: {
                $vuetify?: {
                  theme?: {
                    change?: (n: string) => void;
                    global?: { name?: { value: string } };
                  };
                };
              };
            };
          };
        }
      )?.__vue_app__;
      const vt = vueApp?.config?.globalProperties?.$vuetify?.theme;
      if (vt?.change) vt.change(t);
      else if (vt?.global?.name) vt.global.name.value = t;
    } catch {
      /* fall through to class toggle */
    }
    app.classList.toggle("v-theme--dark", t === "dark");
    app.classList.toggle("v-theme--light", t !== "dark");
  }, theme);
};

/** Wait until a Vuetify dialog finished its enter transition (no sleeps). */
export const waitForDialogSettled = async (page: Page): Promise<void> => {
  const dialog = page.locator(".v-dialog.v-overlay--active, .v-dialog[aria-modal='true']").first();
  await expect(dialog).toBeVisible({ timeout: 10_000 });
  await dialog.evaluate(async (el) => {
    const reduce = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduce) return;
    await new Promise<void>((resolve) => {
      let settled = false;
      const done = (): void => {
        if (settled) return;
        settled = true;
        el.removeEventListener("transitionend", onEnd);
        resolve();
      };
      const onEnd = (ev: TransitionEvent): void => {
        if (ev.target !== el && !el.contains(ev.target as Node)) return;
        done();
      };
      el.addEventListener("transitionend", onEnd);
      // If already idle (no transition queued), resolve on next frames.
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          const style = getComputedStyle(el);
          const dur = style.transitionDuration || "0s";
          if (dur === "0s" || dur === "0ms") done();
        });
      });
    });
  });
};

export const assertNoOverflow = async (page: Page): Promise<void> => {
  const overflow = await page.evaluate(() => {
    const check = (el: Element | null): string | null => {
      if (!el || !(el instanceof HTMLElement)) return null;
      if (el.scrollWidth > el.clientWidth + 1) {
        return `${el.tagName}.${el.className}: ${el.scrollWidth}>${el.clientWidth}`;
      }
      return null;
    };
    return (
      check(document.body) ||
      check(document.querySelector(".v-main")) ||
      check(document.querySelector(".v-container")) ||
      check(document.documentElement)
    );
  });
  expect(overflow, `horizontal overflow: ${overflow}`).toBeNull();
};

export const assertAxe = async (page: Page): Promise<void> => {
  const results = await new AxeBuilder({ page })
    .withTags(["wcag2a", "wcag2aa"])
    .analyze();
  const bad = results.violations.filter((v) => {
    if (v.impact !== "serious" && v.impact !== "critical") return false;
    // Kit AutocompleteSearch: sticky search in #prepend-item sits inside role=listbox.
    // Owner: kit wins over an axe-driven v-autocomplete rewrite — drop only that
    // aria-required-children hit (tied to data-testid=autocomplete-search-input).
    if (v.id === "aria-required-children") {
      const kitSearch = v.nodes.some((n) => {
        const blob = `${n.html} ${n.target.join(" ")}`;
        return blob.includes("autocomplete-search-input");
      });
      if (kitSearch) return false;
    }
    return true;
  });
  expect(
    bad,
    bad.map((v) => `${v.id}: ${v.help}`).join("; "),
  ).toEqual([]);
};

export const runScreen = async (
  page: Page,
  testInfo: TestInfo,
  opts: ScreenCheckOptions,
): Promise<void> => {
  const consoleErrors: string[] = [];
  const failed: string[] = [];
  const allow = opts.allowFailedURLs ?? [];

  const allowedNoise = (text: string): boolean => {
    if (text.includes("fonts.googleapis.com") || text.includes("fonts.gstatic.com")) return true;
    if (text.includes("net::ERR_FAILED") && text.includes("fonts.")) return true;
    if (text.includes("429") || /\bstatus of 429\b/i.test(text)) return true;
    if (allow.some((a) => text.includes(a))) return true;
    if (allow.length && /\bstatus of 422\b/i.test(text)) return true;
    return false;
  };

  page.on("console", (msg) => {
    if (msg.type() !== "error") return;
    const text = msg.text();
    if (allowedNoise(text)) return;
    consoleErrors.push(text);
  });
  page.on("pageerror", (err) => {
    const text = String(err);
    if (!text || text === "undefined") return;
    if (allowedNoise(text)) return;
    consoleErrors.push(text);
  });
  page.on("response", (res) => {
    if (res.status() >= 400) {
      const url = res.url();
      if (allow.some((a) => url.includes(a))) return;
      if (url.includes("favicon") || url.includes("fonts.googleapis.com") || url.includes("fonts.gstatic.com")) return;
      if (res.status() === 429) return;
      failed.push(`${res.status()} ${url}`);
    }
  });

  const theme = projectTheme(testInfo.project.name);
  await applyTheme(page, theme);

  const readyList = Array.isArray(opts.ready) ? opts.ready : [opts.ready];
  for (const sel of readyList) {
    await expect(page.locator(sel).first()).toBeVisible({ timeout: 45_000 });
  }

  if (opts.interact) await opts.interact(page);

  const shotDir = join(here, "..", "screens", testInfo.project.name);
  mkdirSync(shotDir, { recursive: true });
  await page.screenshot({
    path: join(shotDir, `${opts.name}.png`),
    fullPage: true,
  });

  await assertNoOverflow(page);
  await assertAxe(page);

  expect(consoleErrors, consoleErrors.join("\n")).toEqual([]);
  expect(failed, failed.join("\n")).toEqual([]);
};
