import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flushPromises, mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import en from "@/features/search/locales/en.json";
import { ApiProblemError } from "@/api/client";

const routerPush = vi.fn();
const routerReplace = vi.fn();
let routeQuery: Record<string, string> = {};

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: routeQuery }),
  useRouter: () => ({ replace: routerReplace, push: routerPush }),
}));
vi.mock("@/api/settings", () => ({ getSettings: vi.fn() }));
vi.mock("@/api/outbox", () => ({ searchAccounts: vi.fn(async () => []) }));
vi.mock("@/features/search/helpers/api", async (orig) => ({
  ...(await orig<typeof import("@/features/search/helpers/api")>()),
  searchMessages: vi.fn(),
  reindexAccount: vi.fn(),
  // G1: picking an account checks semantic search; an unmocked request would hang teardown.
  getEmbeddingsStatus: vi.fn(async () => ({
    accountId: "acc_1", enabled: false, reason: "switch_off", messages: 0, chunks: 0, indexedMessages: 0, maxChunks: 50000,
  })),
}));
// ColManager → settings.load() would hit /v1/me/settings and hang teardown.
vi.mock("@/stores/settings", () => ({
  useSettingsStore: () => ({
    load: vi.fn(async () => undefined),
    getTable: () => undefined,
    setOrder: vi.fn(),
    setVisibility: vi.fn(),
    setWidths: vi.fn(),
    resetWidths: vi.fn(),
  }),
}));

import { getSettings } from "@/api/settings";
import { reindexAccount, searchMessages, fullTextSwitchOf, type SearchHit } from "@/features/search/helpers/api";
import { snippetSegments } from "@/features/search/helpers/snippet";
import { useFullTextSearch } from "@/features/search/composables/useFullTextSearch";
import SearchPage from "@/features/search/pages/SearchPage.vue";
import FullTextReindexButton from "@/features/search/components/FullTextReindexButton.vue";
import SnippetText from "@/features/search/components/SnippetText.vue";
import { useSessionStore } from "@/stores/session";
import { useConfirmStore } from "@/composables/useConfirm";

const i18n = () =>
  createI18n({
    legacy: false,
    locale: "en",
    missingWarn: false,
    fallbackWarn: false,
    messages: {
      en: {
        ...en,
        tables: { search_placeholder: "Search", no_results: "No results" },
        common: { close: "Close", states: { loading: "Loading", refreshing: "Refreshing" } },
        problems: { internal_error: "Internal error", feature_disabled: "Feature disabled" },
      },
    },
  });

const settingsWith = (on: boolean) => ({
  body: { values: {}, switches: { "search.fullText": { value: on, source: "organization", scope: "organization" } }, version: 1 },
  etag: '"v1"',
});

const hit = (over: Partial<SearchHit> = {}): SearchHit => ({
  messageId: "msg_1",
  accountId: "acc_1",
  mailboxId: "mbx_1",
  path: "INBOX",
  subject: "Invoice 2026-1043",
  from: "Billing <billing@example.com>",
  to: ["reception@example.com"],
  date: "2026-10-27T09:55:12Z",
  size: 100,
  snippet: "…find the invoice attached…",
  highlights: [{ start: 10, end: 17 }],
  score: 1.5,
  ...over,
});

const seedMe = (role: string): void => {
  useSessionStore().me = {
    kind: "user",
    mfa: true,
    permissions: [],
    memberships: [],
    role,
    user: { id: "u1", email: "a@b.c", name: "Ada", locale: "en", instanceRole: "member", status: "active" },
  } as never;
};

const withSetup = <T>(fn: () => T): T => {
  let result!: T;
  mount({ setup: () => { result = fn(); return () => null; } }, { global: { plugins: [i18n()] } });
  return result;
};

beforeEach(() => {
  routeQuery = {};
  vi.mocked(getSettings).mockResolvedValue(settingsWith(true) as never);
  vi.mocked(searchMessages).mockResolvedValue({ data: [hit()], total: 1, totalCapped: false });
});

afterEach(() => {
  vi.clearAllMocks();
  document.body.innerHTML = "";
});

describe("snippetSegments", () => {
  it("splits on code points and ignores bad ranges", () => {
    expect(snippetSegments("…find the invoice", [{ start: 10, end: 17 }])).toEqual([
      { text: "…find the ", match: false },
      { text: "invoice", match: true },
    ]);
    // An astral character counts as one code point (two UTF-16 units).
    expect(snippetSegments("😀 café ok", [{ start: 2, end: 6 }])).toEqual([
      { text: "😀 ", match: false },
      { text: "café", match: true },
      { text: " ok", match: false },
    ]);
    expect(snippetSegments("abc", [{ start: 2, end: 9 }, { start: -1, end: 1 }, { start: 1, end: 1 }])).toEqual([
      { text: "abc", match: false },
    ]);
    expect(snippetSegments("abcdef", [{ start: 3, end: 4 }, { start: 0, end: 1 }, { start: 0, end: 2 }])).toEqual([
      { text: "a", match: true },
      { text: "bc", match: false },
      { text: "d", match: true },
      { text: "ef", match: false },
    ]);
    expect(snippetSegments("", [])).toEqual([]);
  });

  it("renders matches as mark, never as HTML", () => {
    const w = mount(SnippetText, {
      props: { text: "<b>x</b> invoice", highlights: [{ start: 9, end: 16 }] },
      global: { plugins: [i18n()] },
    });
    expect(w.find("mark").text()).toBe("invoice");
    expect(w.find("b").exists()).toBe(false);
    expect(w.text()).toContain("<b>x</b>");
  });
});

describe("fullTextSwitchOf", () => {
  it("reads only a boolean account value", () => {
    expect(fullTextSwitchOf({ "search.fullText": false })).toBe(false);
    expect(fullTextSwitchOf({ "search.fullText": true })).toBe(true);
    expect(fullTextSwitchOf({ "search.fullText": "yes" })).toBeUndefined();
    expect(fullTextSwitchOf(null)).toBeUndefined();
  });
});

describe("useFullTextSearch", () => {
  it("does not search without a query, then searches from page 1 with the filters", async () => {
    const s = withSetup(() => useFullTextSearch());
    await s.init();
    expect(s.enabled.value).toBe(true);
    expect(searchMessages).not.toHaveBeenCalled();
    expect(s.items.value).toEqual([]);

    s.page.value = 3;
    s.accountId.value = "acc_1";
    s.from.value = "2026-10-01T00:00:00.000Z";
    s.setQuery("  invoice  ");
    await flushPromises();
    expect(searchMessages).toHaveBeenCalledWith({
      q: "invoice", accountId: "acc_1", from: "2026-10-01T00:00:00.000Z", to: undefined, page: 1, limit: 25,
    });
    expect(s.items.value).toHaveLength(1);
    expect(s.total.value).toBe(1);
    expect(routerReplace).toHaveBeenLastCalledWith({
      query: { q: "invoice", accountId: "acc_1", from: "2026-10-01T00:00:00.000Z" },
    });
    s.clearFilters();
    await flushPromises();
    expect(vi.mocked(searchMessages).mock.calls.at(-1)?.[0]).toMatchObject({ accountId: undefined, from: undefined });
  });

  it("restores the query from the URL and loads at once", async () => {
    routeQuery = { q: "fattura", page: "2" };
    const s = withSetup(() => useFullTextSearch());
    await s.init();
    expect(searchMessages).toHaveBeenCalledWith(expect.objectContaining({ q: "fattura", page: 2 }));
  });

  it("switches to the disabled state on feature_disabled and reports other errors", async () => {
    routeQuery = { q: "x" };
    vi.mocked(searchMessages).mockRejectedValueOnce(new ApiProblemError({ status: 403, code: "feature_disabled" }));
    const s = withSetup(() => useFullTextSearch());
    await s.init();
    expect(s.enabled.value).toBe(false);
    expect(s.loadError.value).toBeNull();

    vi.mocked(searchMessages).mockRejectedValueOnce(new ApiProblemError({ status: 500, code: "internal_error" }));
    const s2 = withSetup(() => useFullTextSearch());
    await s2.init();
    expect(s2.enabled.value).toBe(true);
    expect(s2.loadError.value).toBe("Internal error");
  });

  it("reports a failed switch check and never searches", async () => {
    routeQuery = { q: "x" };
    vi.mocked(getSettings).mockRejectedValueOnce(new ApiProblemError({ status: 500, code: "internal_error" }));
    const s = withSetup(() => useFullTextSearch());
    await s.init();
    expect(s.statusError.value).toBe("Internal error");
    expect(searchMessages).not.toHaveBeenCalled();
  });
});

describe("SearchPage", () => {
  it("explains the off switch and links admins to Settings", async () => {
    seedMe("admin");
    vi.mocked(getSettings).mockResolvedValue(settingsWith(false) as never);
    const w = mount(SearchPage, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="search-disabled"]').text()).toContain("Full-text search is off");
    await w.find('[data-testid="search-open-settings"]').trigger("click");
    expect(routerPush).toHaveBeenCalledWith({ name: "settings" });
    w.unmount();
  });

  it("tells viewers to ask an admin instead", async () => {
    seedMe("viewer");
    vi.mocked(getSettings).mockResolvedValue(settingsWith(false) as never);
    const w = mount(SearchPage, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="search-open-settings"]').exists()).toBe(false);
    expect(w.text()).toContain("Ask an owner or admin");
    w.unmount();
  });

  it("shows the prompt, then results with highlights that open the reader", async () => {
    seedMe("viewer");
    const w = mount(SearchPage, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="search-no-data"]').text()).toContain("Search all accounts");

    const input = w.find("input[type='text']");
    await input.setValue("invoice");
    await new Promise((r) => setTimeout(r, 400)); // the table's search box debounces
    await flushPromises();
    expect(searchMessages).toHaveBeenCalledWith(expect.objectContaining({ q: "invoice", page: 1 }));
    expect(w.find("mark").text()).toBe("invoice");
    expect(w.text()).toContain("Invoice 2026-1043");

    // Rows on desktop, cards below the mobile breakpoint: either opens the reader.
    const row = w.find('[data-testid="row-msg_1"]');
    await (row.exists() ? row : w.find('[data-testid="card-msg_1"] > *')).trigger("click");
    expect(routerPush).toHaveBeenCalledWith({
      name: "messages",
      query: { accountId: "acc_1", mailboxId: "mbx_1", messageId: "msg_1" },
    });
    w.unmount();
  });
});

describe("FullTextReindexButton", () => {
  it("is hidden while the organisation switch is off or the account opted out", async () => {
    vi.mocked(getSettings).mockResolvedValue(settingsWith(false) as never);
    let w = mount(FullTextReindexButton, { props: { accountId: "acc_1" }, global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="fulltext-reindex"]').exists()).toBe(false);
    w.unmount();

    vi.mocked(getSettings).mockResolvedValue(settingsWith(true) as never);
    w = mount(FullTextReindexButton, { props: { accountId: "acc_1", optedOut: true }, global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="fulltext-reindex"]').exists()).toBe(false);
    w.unmount();
  });

  it("asks before queueing the reindex", async () => {
    vi.mocked(reindexAccount).mockResolvedValue({ job: { id: "job_1", kind: "fulltext.reindex", state: "queued" } });
    const w = mount(FullTextReindexButton, { props: { accountId: "acc_1" }, global: { plugins: [i18n()] } });
    await flushPromises();
    const confirm = useConfirmStore();
    const ask = vi.spyOn(confirm, "ask").mockResolvedValueOnce(false).mockResolvedValueOnce(true);

    await w.find('[data-testid="fulltext-reindex"]').trigger("click");
    await flushPromises();
    expect(ask).toHaveBeenCalledTimes(1);
    expect(reindexAccount).not.toHaveBeenCalled();

    await w.find('[data-testid="fulltext-reindex"]').trigger("click");
    await flushPromises();
    expect(reindexAccount).toHaveBeenCalledWith("acc_1");
    w.unmount();
  });
});
