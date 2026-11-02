import { beforeEach, describe, expect, it, vi } from "vitest";
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
vi.mock("@/api/outbox", () => ({
  searchAccounts: vi.fn(async () => [{ id: "acc_1", name: "Reception", email: "reception@example.com" }]),
}));
vi.mock("@/features/search/helpers/api", async (orig) => ({
  ...(await orig<typeof import("@/features/search/helpers/api")>()),
  searchMessages: vi.fn(),
  getEmbeddingsStatus: vi.fn(),
  semanticSearch: vi.fn(),
  askMailbox: vi.fn(),
  reindexEmbeddings: vi.fn(),
}));
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
import {
  askMailbox,
  getEmbeddingsStatus,
  reindexEmbeddings,
  searchMessages,
  semanticSearch,
  type AskResult,
  type EmbeddingsStatus,
} from "@/features/search/helpers/api";
import { answerParts } from "@/features/search/composables/useAskMailbox";
import { semanticReasonKey } from "@/features/search/composables/useSemanticStatus";
import { semanticRow, useFullTextSearch } from "@/features/search/composables/useFullTextSearch";
import AskPanel from "@/features/search/components/AskPanel.vue";
import SemanticReindexButton from "@/features/search/components/SemanticReindexButton.vue";
import SearchPage from "@/features/search/pages/SearchPage.vue";
import { ACCOUNT_SWITCH_KEYS, switchOn } from "@/features/accounts/helpers/accountState";
import { useConfirmStore } from "@/composables/useConfirm";

const A = "msg_01m5ck6pf04wm1cyx6q10s4x5w";
const B = "msg_01m5ck6pf04wm1cyx6q10s4x5x";
const FAKE = "msg_zzzzzzzzzzzzzzzzzzzzzzzzzz";

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

const settingsOn = {
  body: { values: {}, switches: { "search.fullText": { value: true, source: "organization", scope: "organization" } }, version: 1 },
  etag: '"v1"',
};

const status = (over: Partial<EmbeddingsStatus> = {}): EmbeddingsStatus => ({
  accountId: "acc_1",
  enabled: true,
  model: "text-embedding-3-small",
  messages: 12,
  chunks: 30,
  indexedMessages: 20,
  maxChunks: 50000,
  ...over,
});

const answer = (over: Partial<AskResult> = {}): AskResult => ({
  answer: `The assembly is on 14 November [${A}].`,
  citations: [
    { messageId: A, accountId: "acc_1", mailboxId: "mbx_1", subject: "Assembly", from: "Admin", date: "2026-10-27T09:55:12Z", score: 0.83 },
  ],
  excerpts: 3,
  model: "text-embedding-3-small",
  ...over,
});

const withSetup = <T>(fn: () => T): T => {
  let result!: T;
  mount({ setup: () => { result = fn(); return () => null; } }, { global: { plugins: [i18n()] } });
  return result;
};

beforeEach(() => {
  routeQuery = {};
  vi.mocked(getSettings).mockResolvedValue(settingsOn as never);
  vi.mocked(searchMessages).mockResolvedValue({ data: [], total: 0, totalCapped: false });
  vi.mocked(getEmbeddingsStatus).mockResolvedValue(status());
  vi.mocked(semanticSearch).mockResolvedValue({
    data: [{ messageId: A, accountId: "acc_1", mailboxId: "mbx_1", path: "INBOX", subject: "Assembly", from: "Admin", date: "2026-10-27T09:55:12Z", snippet: "the assembly is called…", score: 0.8312 }],
    model: "text-embedding-3-small",
    chunks: 30,
  });
  vi.mocked(askMailbox).mockReset();
  vi.mocked(reindexEmbeddings).mockReset();
  routerPush.mockReset();
});

describe("helpers", () => {
  it("splits an answer into text and numbered citations", () => {
    const parts = answerParts(`Monday [${A}] and Tuesday [${B}, ${A}]. Made up [${FAKE}].`, [A, B]);
    expect(parts).toEqual([
      { kind: "text", text: "Monday " },
      { kind: "cite", messageId: A, n: 1 },
      { kind: "text", text: " and Tuesday " },
      { kind: "cite", messageId: B, n: 2 },
      { kind: "cite", messageId: A, n: 1 },
      { kind: "text", text: ". Made up " },
      { kind: "text", text: "." },
    ]);
    expect(answerParts("No citations.", [])).toEqual([{ kind: "text", text: "No citations." }]);
  });

  it("maps reasons and semantic hits", () => {
    expect(semanticReasonKey("not_configured")).toBe("search.meaning_reason_not_configured");
    expect(semanticReasonKey("fulltext_off")).toBe("search.meaning_reason_fulltext_off");
    expect(semanticReasonKey("ai_off")).toBe("search.meaning_reason_ai_off");
    expect(semanticReasonKey(undefined)).toBe("search.meaning_reason_switch_off");
    const row = semanticRow({ messageId: A, accountId: "acc_1", mailboxId: "mbx_1", path: "INBOX", subject: "S", from: "F", date: "d", snippet: "x", score: 0.5 });
    expect(row.highlights).toEqual([]);
    expect(row.to).toEqual([]);
    expect(row.score).toBe(0.5);
  });

  it("shows organisation-master switches as on until the account opts out", () => {
    expect(ACCOUNT_SWITCH_KEYS).toContain("search.fullText");
    expect(ACCOUNT_SWITCH_KEYS).toContain("ai.embeddings");
    expect(switchOn({}, "ai.embeddings")).toBe(true);
    expect(switchOn({ "ai.embeddings": false }, "ai.embeddings")).toBe(false);
    expect(switchOn(undefined, "search.fullText")).toBe(true);
    expect(switchOn({}, "sandbox.enabled")).toBe(false);
  });
});

describe("useFullTextSearch meaning mode", () => {
  it("offers meaning only for an account where it is available, and searches that account", async () => {
    const s = withSetup(() => useFullTextSearch());
    await s.init();
    s.setMode("meaning");
    expect(s.mode.value).toBe("text"); // no account yet

    s.accountId.value = "acc_1";
    await flushPromises();
    expect(getEmbeddingsStatus).toHaveBeenCalledWith("acc_1");
    expect(s.semanticAvailable.value).toBe(true);
    s.q.value = "when is the assembly";
    s.setMode("meaning");
    await flushPromises();
    expect(semanticSearch).toHaveBeenLastCalledWith({ accountId: "acc_1", q: "when is the assembly", limit: 25, from: undefined, to: undefined });
    expect(s.items.value[0].messageId).toBe(A);
    expect(s.total.value).toBe(1);
    expect(routerReplace).toHaveBeenLastCalledWith({ query: expect.objectContaining({ mode: "meaning", accountId: "acc_1" }) });

    // Another account without semantic search: back to text.
    vi.mocked(getEmbeddingsStatus).mockResolvedValue(status({ accountId: "acc_2", enabled: false, reason: "switch_off" }));
    s.accountId.value = "acc_2";
    await flushPromises();
    expect(s.mode.value).toBe("text");
  });

  it("surfaces a semantic search error and re-checks the switch on feature_disabled", async () => {
    routeQuery = { accountId: "acc_1", mode: "meaning", q: "roof" };
    vi.mocked(semanticSearch).mockRejectedValue(
      new ApiProblemError({ status: 403, code: "feature_disabled", title: "Off", detail: "Semantic search is switched off for this account." }),
    );
    const s = withSetup(() => useFullTextSearch());
    await s.init();
    await flushPromises();
    expect(s.mode.value).toBe("meaning");
    expect(s.loadError.value).toBeTruthy();
    expect(s.enabled.value).toBe(true); // full-text stays usable
    expect(vi.mocked(getEmbeddingsStatus).mock.calls.length).toBeGreaterThanOrEqual(2);
  });
});

describe("AskPanel", () => {
  it("asks, shows the answer with citations and opens a source", async () => {
    vi.mocked(askMailbox).mockResolvedValue(answer());
    const w = mount(AskPanel, { props: { accountId: "acc_1", accountLabel: "Reception" }, global: { plugins: [i18n()] } });
    expect(w.text()).toContain("Answers come from Reception only.");
    const submit = w.find('[data-testid="ask-submit"]');
    expect(submit.attributes("disabled")).toBeDefined();
    await w.find('[data-testid="ask-question"] textarea').setValue("When is the next assembly?");
    await w.find('[data-testid="ask-question"] textarea').trigger("keydown", { key: "Enter", ctrlKey: true });
    await flushPromises();
    expect(askMailbox).toHaveBeenCalledWith("acc_1", "When is the next assembly?", { from: undefined, to: undefined });
    expect(w.find('[data-testid="ask-range"]').exists()).toBe(false);
    const ans = w.find('[data-testid="ask-answer"]');
    expect(ans.text()).toContain("The assembly is on 14 November");
    expect(ans.text()).not.toContain(A); // IDs become numbered markers, never raw text or HTML
    expect(ans.find(".cite-ref").text()).toBe("[1]");
    expect(w.findAll('[data-testid="ask-citation"]')).toHaveLength(1);
    await w.find('[data-testid="ask-citation"]').trigger("click");
    expect(w.emitted("open")?.[0]?.[0]).toMatchObject({ messageId: A, mailboxId: "mbx_1" });
    await ans.find(".cite-ref").trigger("click");
    expect(w.emitted("open")).toHaveLength(2);
    await w.find('[data-testid="ask-close"]').trigger("click");
    expect(w.emitted("close")).toHaveLength(1);
    w.unmount();
  });

  it("explains an empty account and shows errors", async () => {
    vi.mocked(askMailbox).mockResolvedValueOnce(answer({ answer: "", citations: [], excerpts: 0 }));
    const w = mount(AskPanel, { props: { accountId: "acc_1", accountLabel: "Reception" }, global: { plugins: [i18n()] } });
    await w.find('[data-testid="ask-question"] textarea').setValue("anything?");
    await w.find('[data-testid="ask-submit"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="ask-nothing"]').exists()).toBe(true);

    vi.mocked(askMailbox).mockRejectedValueOnce(
      new ApiProblemError({ status: 429, code: "rate_limited", title: "Rate limited", detail: "Too many questions." }),
    );
    await w.setProps({ accountId: "acc_2" }); // another account: the old answer goes
    await w.find('[data-testid="ask-submit"]').trigger("click");
    await flushPromises();
    expect(w.find('[data-testid="ask-answer"]').exists()).toBe(false);
    expect(w.find('[data-testid="ask-error"]').exists()).toBe(true);
    w.unmount();
  });
});

describe("SearchPage meaning and ask", () => {
  it("keeps Meaning and Ask off until an account with semantic search is picked", async () => {
    const w = mount(SearchPage, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="search-mode-meaning"]').attributes("disabled")).toBeDefined();
    expect(w.find('[data-testid="ask-open"]').attributes("disabled")).toBeDefined();
    expect(w.find('[data-testid="search-meaning-note"]').text()).toContain("Pick an account");
    w.unmount();
  });

  it("searches by meaning and answers questions for the picked account", async () => {
    routeQuery = { accountId: "acc_1", q: "assembly", mode: "meaning", from: "2026-10-01T00:00:00Z" };
    vi.mocked(askMailbox).mockResolvedValue(answer());
    const w = mount(SearchPage, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(semanticSearch).toHaveBeenCalled();
    expect(w.text()).toContain("Closeness");
    expect(w.text()).toContain("83%");
    expect(w.find('[data-testid="search-meaning-note"]').text()).toContain("12 of 20");

    await w.find('[data-testid="ask-open"]').trigger("click");
    await flushPromises();
    const panel = w.find('[data-testid="ask-panel"]');
    expect(panel.exists()).toBe(true);
    await panel.find("textarea").setValue("When is the next assembly?");
    await panel.find('[data-testid="ask-submit"]').trigger("click");
    await flushPromises();
    // The page's date filter narrows the excerpts (the way past the chunk cap of a large mailbox).
    expect(panel.find('[data-testid="ask-range"]').exists()).toBe(true);
    expect(askMailbox).toHaveBeenCalledWith("acc_1", "When is the next assembly?", { from: "2026-10-01T00:00:00Z", to: "" });
    await w.find('[data-testid="ask-citation"]').trigger("click");
    expect(routerPush).toHaveBeenCalledWith({ name: "messages", query: { accountId: "acc_1", mailboxId: "mbx_1", messageId: A } });
    w.unmount();
  });

  it("shows why Meaning is unavailable", async () => {
    routeQuery = { accountId: "acc_1" };
    vi.mocked(getEmbeddingsStatus).mockResolvedValue(status({ enabled: false, reason: "not_configured", messages: 0, chunks: 0 }));
    const w = mount(SearchPage, { global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="search-meaning-note"]').text()).toContain("embeddings endpoint");
    expect(w.find('[data-testid="search-mode-meaning"]').attributes("disabled")).toBeDefined();
    w.unmount();
  });
});

describe("SemanticReindexButton", () => {
  it("is hidden while semantic search is unavailable", async () => {
    vi.mocked(getEmbeddingsStatus).mockResolvedValue(status({ enabled: false, reason: "switch_off" }));
    const w = mount(SemanticReindexButton, { props: { accountId: "acc_1" }, global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.find('[data-testid="semantic-reindex"]').exists()).toBe(false);
    w.unmount();
  });

  it("shows coverage and asks before queueing", async () => {
    vi.mocked(reindexEmbeddings).mockResolvedValue({ job: { id: "job_1", kind: "embeddings.reindex", state: "queued" } });
    const w = mount(SemanticReindexButton, { props: { accountId: "acc_1" }, global: { plugins: [i18n()] } });
    await flushPromises();
    expect(w.text()).toContain("12 of 20");
    const ask = vi.spyOn(useConfirmStore(), "ask").mockResolvedValueOnce(false).mockResolvedValueOnce(true);
    await w.find('[data-testid="semantic-reindex-button"]').trigger("click");
    await flushPromises();
    expect(ask).toHaveBeenCalledTimes(1);
    expect(reindexEmbeddings).not.toHaveBeenCalled();
    await w.find('[data-testid="semantic-reindex-button"]').trigger("click");
    await flushPromises();
    expect(reindexEmbeddings).toHaveBeenCalledWith("acc_1");
    // The account's switches changed: check again.
    await w.setProps({ revision: 2 });
    await flushPromises();
    expect(vi.mocked(getEmbeddingsStatus).mock.calls.length).toBeGreaterThanOrEqual(2);
    w.unmount();
  });
});
