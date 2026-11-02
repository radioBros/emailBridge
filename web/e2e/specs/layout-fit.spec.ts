import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import type { Page } from "@playwright/test";
import { test, expect } from "../fixtures/auth";
import type { StackInfo } from "../fixtures/stack";

/**
 * Layout fit — every page with a table fits the viewport exactly (measured fill height,
 * composables/useFillHeight): on desktop the page does not scroll, the table footer sits on the
 * card's bottom edge and the table body ends on the footer; nowhere does the page scroll sideways.
 * On phones the card list scrolls in a box as tall as the screen below the app bar (the page
 * scrolls the header away, then the list fills the screen), stays virtual, and no card cuts a long
 * value off at its edge.
 *
 * Screenshots (viewport only) land in test-results/layout-fit/<project>/<case>.png for review.
 */

const shotDir = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "test-results", "layout-fit");

/** Same value as TABLE_FILL_MIN_HEIGHT (src/core/config/UiLayout.ts). */
const FILL_MIN = 360;

type Case = {
  name: string;
  path: (stack: StackInfo) => string | null;
  /** After the page is ready: open a tab, pick a folder, … */
  setup?: (page: Page) => Promise<void>;
  /** Desktop only. */
  desktopOnly?: boolean;
  /** Why this page legitimately scrolls (content above the table); skips the fit check only. */
  scrolls?: string;
  /** Why the seeded page shows no table (an empty state instead); the box checks then do not apply. */
  noTable?: string;
};

const clickTab = (label: RegExp) => async (page: Page): Promise<void> => {
  await page.getByRole("tab", { name: label }).first().click();
};

const account = (tab?: RegExp, pec = false): Pick<Case, "path" | "setup"> => ({
  path: (s) => {
    const id = pec ? s.ids.pecAccountId : s.ids.accountId;
    return id ? `/accounts/${id}` : null;
  },
  setup: tab ? clickTab(tab) : undefined,
});

const openFirstFolder = async (page: Page): Promise<void> => {
  const folder = page.locator(".messages-folder-tree .v-list-item").filter({ hasText: /inbox/i }).first();
  await folder.waitFor({ state: "visible", timeout: 20_000 });
  await folder.click();
  // Desktop: the fill-height table; phones: the page-scrolled card list.
  await page.locator(".table-fill, main .v-infinite-scroll").first().waitFor({ state: "visible", timeout: 20_000 });
};

const cases: Case[] = [
  { name: "accounts", path: () => "/accounts" },
  { name: "account-events", ...account(/recent events/i) },
  { name: "account-outbox", ...account(/^outbox$/i) },
  {
    name: "account-logs",
    ...account(/protocol logs/i),
    noTable: "the protocol-logs tab is a log viewer, not a table",
  },
  {
    name: "account-exports",
    ...account(/exports/i),
    noTable: "bulk export is off in the seeded organisation (a banner instead of the table)",
  },
  { name: "account-pec", ...account(/^pec$/i, true) },
  {
    name: "messages",
    path: (s) => (s.ids.accountId ? `/messages?accountId=${s.ids.accountId}` : null),
    setup: async (page) => {
      await openFirstFolder(page);
      // Selecting a row shows the bulk bar above the table; clearing it hides the bar again —
      // the table must grow back to the viewport bottom (shrink above → re-measure).
      const tick = page.locator("[data-testid^='row-'] input[type='checkbox'], [data-testid^='card-'] input[type='checkbox']").first();
      if (await tick.count()) {
        await tick.check();
        await page.waitForTimeout(300);
        await tick.uncheck();
      }
    },
  },
  {
    name: "messages-reader",
    desktopOnly: true,
    path: (s) => (s.ids.accountId ? `/messages?accountId=${s.ids.accountId}` : null),
    setup: async (page) => {
      await openFirstFolder(page);
      const row = page.locator("[data-testid^='row-']").first();
      await row.waitFor({ state: "visible", timeout: 20_000 });
      await row.click();
      await page.waitForURL(/messageId=/, { timeout: 20_000 });
    },
  },
  { name: "outbox", path: () => "/outbox" },
  { name: "sandbox", path: () => "/sandbox", noTable: "no captured mail in the seed (empty state)" },
  { name: "events", path: () => "/events" },
  { name: "deliveries", path: () => "/deliveries" },
  {
    name: "queues",
    path: () => "/queues",
    noTable: "the Sending queue is empty in the seed (empty state); queues-webhooks covers the table",
  },
  {
    name: "queues-webhooks",
    path: () => "/queues",
    setup: clickTab(/webhook deliveries/i),
    scrolls: "KPI cards, tabs and filters (~590 px) sit above the table: it is at its floor and the page scrolls by design",
  },
  { name: "logs", path: () => "/logs" },
  { name: "audit", path: () => "/audit" },
  { name: "api-keys", path: () => "/api-keys" },
  { name: "members", path: () => "/members" },
  { name: "organizations", path: () => "/organizations" },
  { name: "webhooks", path: () => "/webhooks" },
  { name: "routes", path: () => "/routes" },
  { name: "rule-lists", path: () => "/rule-lists" },
  { name: "templates", path: () => "/templates" },
  { name: "gateways", path: () => "/gateways" },
  { name: "suppression", path: () => "/suppression" },
  {
    name: "suppression-entries",
    path: (s) => (s.ids.suppressionListId ? `/suppression/${s.ids.suppressionListId}` : null),
  },
  { name: "oauth-apps", path: () => "/oauth-apps" },
  {
    name: "integrations",
    path: () => "/integrations",
    scrolls: "the connector catalogue sits above the installed-instances table, which keeps its 360 px floor",
  },
  { name: "notifications", path: () => "/notifications" },
  {
    name: "cluster",
    path: () => "/cluster",
    scrolls: "two stacked tables (nodes, queues), each with the 360 px floor",
  },
  { name: "health", path: () => "/health/accounts" },
  { name: "search", path: () => "/search", noTable: "full-text search is off in the seed (empty state)" },
];

type Geometry = {
  innerW: number;
  innerH: number;
  scrollW: number;
  scrollH: number;
  boxes: {
    height: number;
    footerGap: number | null;
    bodyGap: number | null;
    bottomGap: number | null;
    /** Cards in the columns beside the table card: how far their bottom is from the table card's. */
    sideGaps: number[];
  }[];
  skeletons: number;
  /** Phone card lists (the infinite-scroll element): own scroll height vs. visible height. */
  lists: { scrollH: number; clientH: number; height: number }[];
  /** The layout's bars, as Vuetify reserves them on the main area (app bar on top, bottom nav). */
  barTop: number;
  barBottom: number;
  /** Rendered list views (CardList body, also when it shows its empty state). */
  views: number;
  /** Cards whose content runs past their edge. */
  cutCards: number;
};

const measure = (page: Page): Promise<Geometry> =>
  page.evaluate(() => {
    const doc = document.scrollingElement ?? document.documentElement;
    const visible = (el: Element): boolean => el.getClientRects().length > 0;
    const boxes = Array.from(document.querySelectorAll<HTMLElement>("main .table-fill"))
      .filter(visible)
      .map((box) => {
        const rect = box.getBoundingClientRect();
        const card = box.closest<HTMLElement>(".app-card");
        const footer = box.querySelector<HTMLElement>(":scope > .table-fill__footer");
        const wrapper = box.querySelector<HTMLElement>(".v-table__wrapper");
        let footerGap: number | null = null;
        let bodyGap: number | null = null;
        if (footer && card) {
          const inner = card.getBoundingClientRect().bottom - parseFloat(getComputedStyle(card).borderBottomWidth || "0");
          footerGap = Math.abs(inner - footer.getBoundingClientRect().bottom);
        }
        if (footer && wrapper) bodyGap = Math.abs(footer.getBoundingClientRect().top - wrapper.getBoundingClientRect().bottom);
        // Blank space at the bottom: viewport bottom − the page's bottom padding − the bottom of
        // what ends the page under the card: the card itself, or what is stacked below it (a
        // second card under the table). Side columns beside it do not count.
        let bottomGap: number | null = null;
        const container = box.closest<HTMLElement>(".v-container");
        if (card && container) {
          let end = card.getBoundingClientRect().bottom;
          for (let node: HTMLElement = card; node !== container && node.parentElement; node = node.parentElement) {
            const bottom = node.getBoundingClientRect().bottom;
            for (let sib = node.nextElementSibling; sib; sib = sib.nextElementSibling) {
              const r = sib.getBoundingClientRect();
              const pos = getComputedStyle(sib).position;
              if (r.height > 0 && r.top >= bottom - 0.5 && pos !== "fixed" && pos !== "absolute") {
                end = Math.max(end, r.bottom);
              }
            }
          }
          bottomGap = window.innerHeight - parseFloat(getComputedStyle(container).paddingBottom || "0") - end;
        }
        // Side columns (Messages → folders, reader): each column's card ends where the table card ends.
        const sideGaps: number[] = [];
        const col = card?.closest<HTMLElement>(".v-col");
        if (card && col?.parentElement) {
          const tableBottom = card.getBoundingClientRect().bottom;
          for (const other of Array.from(col.parentElement.children)) {
            if (other === col || !visible(other)) continue;
            for (const side of Array.from(other.querySelectorAll<HTMLElement>(":scope > .app-card"))) {
              sideGaps.push(Math.abs(side.getBoundingClientRect().bottom - tableBottom));
            }
          }
        }
        return { height: rect.height, footerGap, bodyGap, bottomGap, sideGaps };
      });
    return {
      innerW: window.innerWidth,
      innerH: window.innerHeight,
      scrollW: doc.scrollWidth,
      scrollH: doc.scrollHeight,
      boxes,
      skeletons: document.querySelectorAll("main .v-skeleton-loader").length,
      lists: Array.from(document.querySelectorAll<HTMLElement>("main .v-infinite-scroll"))
        .filter(visible)
        .map((l) => ({ scrollH: l.scrollHeight, clientH: l.clientHeight, height: l.getBoundingClientRect().height })),
      barTop: parseFloat(getComputedStyle(document.querySelector(".v-main") ?? document.body).paddingTop || "0"),
      barBottom: parseFloat(getComputedStyle(document.querySelector(".v-main") ?? document.body).paddingBottom || "0"),
      views: Array.from(document.querySelectorAll("main .table-fill__body")).filter(visible).length,
      cutCards: Array.from(document.querySelectorAll<HTMLElement>("main [data-testid^='card-'] > .v-card"))
        .filter((card) => card.scrollWidth > card.clientWidth + 1).length,
    };
  });

for (const c of cases) {
  test(`layout fit: ${c.name}`, async ({ authedPage: page, stack }, testInfo) => {
    const phone = testInfo.project.name.startsWith("phone");
    test.skip(phone && Boolean(c.desktopOnly), "desktop-only case");
    const path = c.path(stack);
    test.skip(!path, "nothing seeded for this case");

    await page.goto(path!);
    await page.locator("main").first().waitFor({ state: "visible" });
    if (c.setup) await c.setup(page);
    // Data in, skeletons gone, fill heights settled (ResizeObserver + rAF).
    await expect.poll(async () => (await measure(page)).skeletons, { timeout: 30_000 }).toBe(0);
    await page.waitForTimeout(400);

    const dir = join(shotDir, testInfo.project.name);
    mkdirSync(dir, { recursive: true });
    await page.screenshot({ path: join(dir, `${c.name}.png`) });

    const g = await measure(page);
    expect(g.scrollW, "no horizontal page overflow").toBeLessThanOrEqual(g.innerW);

    if (phone) {
      // One page scroll: the list never scrolls on its own; long values wrap inside the card.
      console.log(`LISTS ${testInfo.project.name} ${c.name} ${g.views}`);
      if (!c.noTable) expect(g.views, "the page shows a card list (or mark the case noTable)").toBeGreaterThan(0);
      // The list box is as tall as the screen below the app bar (floor FILL_MIN); a short list
      // stays shorter.
      const screenBox = Math.max(FILL_MIN, g.innerH - g.barTop - g.barBottom);
      for (const list of g.lists) {
        expect(list.height, "list box no taller than the screen below the app bar").toBeLessThanOrEqual(screenBox + 2);
        if (list.scrollH > list.clientH + 1) {
          expect(Math.abs(list.height - screenBox), "a full list box fills the screen below the app bar").toBeLessThanOrEqual(2);
        }
      }
      expect(g.cutCards, "no card cuts a value off at its edge").toBe(0);
      return;
    }

    console.log(`BOXES ${testInfo.project.name} ${c.name} ${g.boxes.length}`);
    if (!c.noTable) expect(g.boxes.length, "the page shows a table (or mark the case noTable)").toBeGreaterThan(0);

    for (const box of g.boxes) {
      if (box.footerGap != null) expect(box.footerGap, "footer on the card's bottom edge").toBeLessThanOrEqual(2);
      if (box.bodyGap != null) expect(box.bodyGap, "table body ends on the footer").toBeLessThanOrEqual(2);
      // Above the floor the card reaches the viewport bottom (no blank strip under it).
      if (box.bottomGap != null && box.height > FILL_MIN + 1 && !c.scrolls) {
        expect(Math.abs(box.bottomGap), "card ends at the viewport bottom").toBeLessThanOrEqual(2);
      }
      if (!c.scrolls) {
        for (const gap of box.sideGaps) expect(gap, "side card ends with the table card").toBeLessThanOrEqual(2);
      }
    }
    if (g.boxes.some((b) => b.height <= FILL_MIN + 1)) {
      testInfo.annotations.push({ type: "at-floor", description: c.name });
      console.log(`AT-FLOOR ${testInfo.project.name} ${c.name}`);
    }

    // Desktop: the page itself does not scroll.
    if (!c.scrolls) {
      expect(g.scrollH, "page fits the viewport").toBeLessThanOrEqual(g.innerH + 1);
    }
  });
}

// Phones: the list scrolls inside its screen-tall box, stays virtual (far fewer cards in the DOM
// than loaded) and loads one page at a time as its end nears — never every page at once.
test("phone card list is virtual and loads one page per scroll", async ({ authedPage: page }, testInfo) => {
  test.skip(!testInfo.project.name.startsWith("phone"), "phones only");
  const pages: string[] = [];
  page.on("request", (r) => {
    const url = new URL(r.url());
    if (r.method() === "GET" && url.pathname.endsWith("/v1/events")) pages.push(url.searchParams.get("page") ?? "");
  });
  await page.goto("/events?limit=10");
  const cards = page.locator("main [data-testid^='card-']");
  const list = page.locator("main .v-infinite-scroll").first();
  await cards.first().waitFor({ state: "visible", timeout: 30_000 });
  await page.waitForTimeout(1500);
  expect(pages, "only the first page before scrolling").toEqual(["1"]);

  // Scroll the list's end into reach three times: pages 2, 3 and 4, one after the other.
  for (let next = 2; next <= 4; next++) {
    await list.evaluate((el) => el.scrollTo(0, el.scrollHeight));
    await expect.poll(() => pages.length, { timeout: 15_000 }).toBeGreaterThanOrEqual(next);
    await page.waitForTimeout(800);
  }
  expect(pages.slice(0, 4), "one page at a time, in order").toEqual(["1", "2", "3", "4"]);

  // 40 cards loaded (4 × 10), far fewer in the DOM; the scroller is sized for all of them.
  const rendered = await cards.count();
  console.log(`VIRTUAL rendered=${rendered} pages=${pages.join(",")}`);
  expect(rendered, "virtual: far fewer cards rendered than loaded").toBeLessThanOrEqual(20);
  const geo = await list.evaluate((el) => ({ scrollH: el.scrollHeight, clientH: el.clientHeight }));
  expect(geo.scrollH, "the scroller spans the loaded list").toBeGreaterThan(geo.clientH * 3);
});
