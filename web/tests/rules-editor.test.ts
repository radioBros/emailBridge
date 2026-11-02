import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { defineComponent, h, nextTick, reactive, ref } from "vue";
import { flushPromises, mount } from "@vue/test-utils";
import { createI18n } from "vue-i18n";
import en from "@/features/rules/locales/en.json";

vi.mock("@/api/rules", async (orig) => ({
  ...(await orig<typeof import("@/api/rules")>()),
  compileRuleBuilder: vi.fn(),
  testRule: vi.fn(),
  listTestEvents: vi.fn(),
  listRuleLists: vi.fn(),
  storeEnvSecret: vi.fn(),
}));
vi.mock("@/api/routes", () => ({ expressibleBuilder: vi.fn() }));
vi.mock("@/api/accounts", () => ({ getAccount: vi.fn() }));
vi.mock("@/api/messages", () => ({ listMailboxes: vi.fn(), searchAccounts: vi.fn() }));
// ColManager → settings.load() would hit /v1/me/settings (happy-dom → :3000) and hang teardown.
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

import { compileRuleBuilder, listRuleLists, listTestEvents, testRule } from "@/api/rules";
import { expressibleBuilder } from "@/api/routes";
import { listMailboxes, searchAccounts } from "@/api/messages";
import { useRuleDraft } from "@/features/rules/composables/useRuleDraft";
import { createRuleContext, provideRuleContext } from "@/features/rules/composables/useRuleContext";
import FilterBuilder from "@/features/rules/components/FilterBuilder.vue";
import MapEditor from "@/features/rules/components/MapEditor.vue";
import RuleTestPanel from "@/features/rules/components/RuleTestPanel.vue";
import { parseEntries } from "@/features/rules/model/lists";
import { toCel } from "@/features/rules/model/mapTree";
import { presetById } from "@/features/rules/model/presets";
import type { RuleGroup } from "@/api/rules";

const LEGACY_APP_FILTER = '(type == "message.received") && (isInbox())';

const i18n = () =>
  createI18n({
    legacy: false,
    locale: "en",
    missingWarn: false,
    fallbackWarn: false,
    messages: {
      en: {
        ...en,
        common: { search: "Search", close: "Close", states: { loading: "Loading", refreshing: "Refreshing" } },
        problems: { internal_error: "Internal error" },
      },
    },
  });

/** Mounts `render` inside a component that provides the rule context. */
const withContext = (render: () => ReturnType<typeof h>) =>
  defineComponent({
    setup() {
      provideRuleContext(createRuleContext());
      return render;
    },
  });

const global = () => ({ plugins: [i18n()], directives: { tippy: {} } });

beforeEach(() => {
  vi.mocked(compileRuleBuilder).mockImplementation(async (b: RuleGroup) => ({
    ok: true,
    cel: b.conditions
      .map((c) => ("field" in c ? (c.field === "isInbox" ? "(isInbox())" : `(type == "${String(c.value)}")`) : "(group)"))
      .join(" && "),
  }));
  vi.mocked(testRule).mockResolvedValue({ matched: false, cost: 12, durationMs: 1, trace: [] });
  vi.mocked(listTestEvents).mockResolvedValue({ data: [] });
  vi.mocked(listRuleLists).mockResolvedValue({ data: [] });
  vi.mocked(listMailboxes).mockResolvedValue({ data: [] });
  vi.mocked(searchAccounts).mockResolvedValue([]);
});

afterEach(() => {
  vi.clearAllMocks();
  // Dialogs attach to document.body; clear leftover overlays so happy-dom can tear down.
  document.body.innerHTML = "";
});

describe("useRuleDraft", () => {
  const make = () => useRuleDraft({ kind: ref("endpoint"), targetId: ref("whe_1"), debounceMs: 0 });

  it("opens a builder-made rule in guided + visual mode and compiles through the API", async () => {
    const preset = presetById("keyedInbound")!.apply("webhookKey");
    const d = make();
    await d.init({ filter: LEGACY_APP_FILTER, map: toCel(preset.map!), builder: preset.builder });
    expect(compileRuleBuilder).toHaveBeenCalledWith({
      match: "all",
      conditions: [
        { field: "type", op: "eq", value: "message.received" },
        { field: "isInbox", op: "eq", value: true },
      ],
    });
    expect(d.filterMode.value).toBe("builder");
    expect(d.mapMode.value).toBe("visual");
    expect(d.dirty.value).toBe(false);
    expect(d.ruleTypes.value).toEqual(["message.received"]);
    expect(d.secretNames.value).toEqual(["webhookKey"]);
    expect(d.needs.value).toEqual(["headers"]);
    expect(d.payload()).toMatchObject({ filter: LEGACY_APP_FILTER, builder: { match: "all" } });
  });

  it("recompiles on builder edits and marks the draft dirty", async () => {
    const d = make();
    await d.init(null);
    expect(d.effectiveFilter.value).toBe("");
    d.builder.value.conditions.push({ field: "type", op: "in", value: ["pec.receipt"] });
    d.onBuilderChange();
    await vi.waitFor(() => expect(d.compiled.value).toBe('(type == "pec.receipt")'));
    expect(d.dirty.value).toBe(true);
    expect(d.compiling.value).toBe(false);
  });

  it("shows a compile error from the API", async () => {
    vi.mocked(compileRuleBuilder).mockRejectedValueOnce(new Error("builder nesting exceeds depth 8"));
    const d = make();
    d.builder.value.conditions.push({ field: "subject", op: "eq", value: "x" });
    d.onBuilderChange();
    await vi.waitFor(() => expect(d.compileError.value).toBe("builder nesting exceeds depth 8"));
  });

  it("opens hand-written CEL in code mode when the builder cannot show it", async () => {
    vi.mocked(expressibleBuilder).mockResolvedValue({ expressible: false });
    const d = make();
    await d.init({ filter: 'merge(event, {"a": 1}) != null', map: 'merge(event, {"vip": true})' });
    expect(d.filterMode.value).toBe("code");
    expect(d.filterReason.value).toBe("advanced");
    expect(d.mapMode.value).toBe("code");
    expect(d.mapReason.value).toBe("expression");
    expect(d.mapToVisual()).toBe(false);
  });

  it("round-trips the map between the tree and code", async () => {
    const d = make();
    await d.init(null);
    d.applyPreset(presetById("keyedStatus")!.apply("statusKey"));
    expect(d.effectiveMap.value).toBe('{\n  "key": env.statusKey,\n  "body": event\n}');
    d.mapToCode();
    expect(d.mapMode.value).toBe("code");
    d.mapCode.value = '{\n  "key": env.statusKey,\n  "body": event,\n  "v": 2\n}';
    expect(d.mapToVisual()).toBe(true);
    expect(d.mapRoot.value).toMatchObject({ kind: "object" });
    expect(d.effectiveMap.value).toContain('"v": 2');
    d.mapCode.value = '{"a": 1 // note\n}';
    d.mapToCode();
    expect(d.mapCode.value).toContain('"v": 2');
  });

  it("goes back from code to the builder through /expressible", async () => {
    vi.mocked(expressibleBuilder).mockResolvedValue({
      expressible: true,
      builder: { match: "all", conditions: [{ field: "type", op: "eq", value: "message.sent" }] },
    });
    const d = make();
    await d.init(null);
    d.toCode();
    d.filterCode.value = 'type == "message.sent"';
    expect(await d.toBuilder()).toBe(true);
    expect(d.filterMode.value).toBe("builder");
    expect(d.compiled.value).toBe('(type == "message.sent")');
  });

  it("evaluates the DRAFT live against the test source and maps inline errors", async () => {
    vi.mocked(testRule).mockResolvedValue({
      matched: false,
      cost: 3,
      durationMs: 1,
      error: "undeclared reference to 'nope'",
      errorAt: { path: "filter", line: 2, column: 3 },
      trace: [],
    });
    const d = make();
    await d.init(null);
    d.toCode();
    d.filterCode.value = 'type == "message.received" &&\n  nope()';
    d.source.value = { mode: "sample", type: "message.received" };
    await vi.waitFor(() => expect(testRule).toHaveBeenCalled());
    expect(vi.mocked(testRule).mock.calls.at(-1)).toEqual([
      "endpoint",
      "whe_1",
      { sample: "message.received", filter: 'type == "message.received" &&\n  nope()', map: "" },
    ]);
    await vi.waitFor(() => expect(d.filterErrors.value).toEqual([{ line: 2, column: 3, message: "undeclared reference to 'nope'" }]));
    expect(d.mapErrors.value).toEqual([]);
  });
});

describe("FilterBuilder", () => {
  it("adds conditions and nested groups and reports every change", async () => {
    const group = reactive<RuleGroup>({ match: "all", conditions: [] });
    const changes = vi.fn();
    const Host = withContext(() =>
      h(FilterBuilder, { group, eventTypes: ["message.received"], onChange: changes }),
    );
    const w = mount(Host, { global: global() });
    await flushPromises();
    expect(w.text()).toContain("No conditions yet");
    await w.get('[data-testid="rule-add-condition"]').trigger("click");
    expect(group.conditions).toEqual([{ field: "type", op: "in", value: ["message.received"] }]);
    await w.get('[data-testid="rule-add-group"]').trigger("click");
    await nextTick();
    expect(group.conditions[1]).toMatchObject({ match: "any" });
    expect(w.find('[data-testid="rule-group-2"]').exists()).toBe(true);
    expect(w.text()).toContain("and");
    expect(changes).toHaveBeenCalledTimes(2);
    await w.findAll('[data-testid="rule-condition-remove"]')[0]!.trigger("click");
    expect(group.conditions).toHaveLength(1);
    w.unmount();
  });
});

describe("MapEditor", () => {
  it("starts from a preset shape and edits keys in place", async () => {
    const roots: unknown[] = [];
    const Host = withContext(() =>
      h(MapEditor, {
        root: null,
        eventTypes: ["message.received"],
        secretNames: [],
        "onUpdate:root": (n: unknown) => roots.push(n),
      }),
    );
    const w = mount(Host, { global: global() });
    expect(w.find('[data-testid="map-empty"]').exists()).toBe(true);
    await w.get('[data-testid="map-start-slim"]').trigger("click");
    expect(roots[0]).toMatchObject({ kind: "object" });
    expect(toCel(roots[0] as never)).toContain('"subject": data.subject');
    w.unmount();
  });
});

describe("RuleTestPanel", () => {
  it("lists recent real events and shows NO MATCH with the per-condition trace", async () => {
    vi.mocked(listTestEvents).mockResolvedValue({
      data: [{ id: "evt_1", type: "message.received", time: "2026-10-27T09:00:00Z", subject: "Udienza 12 novembre" }],
    });
    const updates: unknown[] = [];
    const Host = withContext(() =>
      h(RuleTestPanel, {
        source: { mode: "recent", eventId: "evt_1" },
        result: {
          matched: false,
          cost: 42,
          durationMs: 2,
          trace: [
            { expr: 'type == "message.received"', result: true },
            { expr: "isInbox()", result: false },
          ],
        },
        failure: null,
        firstLoad: false,
        refreshing: false,
        hasMap: true,
        ruleTypes: ["message.received"],
        "onUpdate:source": (s: unknown) => updates.push(s),
      }),
    );
    const w = mount(Host, { global: global() });
    await flushPromises();
    expect(listTestEvents).toHaveBeenCalledWith({ type: "message.received", accountId: undefined, limit: 30 });
    expect(w.text()).toContain("Udienza 12 novembre");
    expect(w.get('[data-testid="test-verdict"]').text()).toContain("NO MATCH");
    expect(w.get('[data-testid="test-verdict"]').text()).toContain("1 condition(s) false");
    const rows = w.get('[data-testid="test-trace"]').findAll("li");
    expect(rows).toHaveLength(2);
    expect(rows[1]!.text()).toContain("isInbox()");
    expect(rows[1]!.text()).toContain("false");
    await w.get('[data-testid="test-source-sample"]').trigger("click");
    expect(updates.at(-1)).toEqual({ mode: "sample", type: "message.received" });
    w.unmount();
  });
});

describe("named lists", () => {
  it("parses pasted entries per kind", () => {
    expect(parseEntries("Partner.IT\n@studio.it, partner.it;court.it", "domains")).toEqual({
      entries: ["partner.it", "studio.it", "court.it"],
      duplicates: 1,
      tooLong: [],
    });
    expect(parseEntries("Udienza, rinvio\nUdienza, rinvio", "keywords").entries).toEqual(["Udienza, rinvio"]);
    expect(parseEntries("x".repeat(300), "keywords").tooLong).toHaveLength(1);
  });
});

describe("presets with a secret", () => {
  it("stores the secret write-only and puts only its NAME in the rule", async () => {
    const { storeEnvSecret } = await import("@/api/rules");
    vi.mocked(storeEnvSecret).mockResolvedValue({});
    const RuleEditor = (await import("@/features/rules/components/RuleEditor.vue")).default;
    const saves: unknown[][] = [];
    const { VLayout } = await import("vuetify/components");
    // The versions drawer is a navigation drawer: it needs a layout around it, as in the app shell.
    const Host = defineComponent({
      setup: () => () =>
        h(VLayout, null, () =>
          h(RuleEditor, {
            kind: "endpoint",
            targetId: "whe_1",
            rule: null,
            subscribed: ["message.sent"],
            onSave: (...a: unknown[]) => saves.push(a),
          }),
        ),
    });
    // VDialog teleports into document.body (outside the wrapper tree).
    const w = mount(Host, {
      global: { ...global(), stubs: { teleport: false } },
      attachTo: document.body,
    });
    await flushPromises();
    await w.get('[data-testid="rule-presets"]').trigger("click");
    await flushPromises();
    await nextTick();
    const byTestId = (id: string): HTMLElement => {
      const el = document.body.querySelector(`[data-testid="${id}"]`);
      if (!el) throw new Error(`missing [data-testid=${id}] in document.body`);
      return el as HTMLElement;
    };
    byTestId("preset-keyedInbound").click();
    await flushPromises();
    await nextTick();
    const nameInput = byTestId("preset-secret-name").querySelector("input") as HTMLInputElement;
    const valueInput = byTestId("preset-secret-value").querySelector("input") as HTMLInputElement;
    nameInput.value = "studioKey";
    nameInput.dispatchEvent(new Event("input"));
    valueInput.value = "s3cr3t-value";
    valueInput.dispatchEvent(new Event("input"));
    await flushPromises();
    byTestId("preset-apply").click();
    await flushPromises();
    expect(storeEnvSecret).toHaveBeenCalledWith("studioKey", "s3cr3t-value");
    expect(w.text()).toContain("message.received"); // missing subscription → "will subscribe"
    await vi.waitFor(() => expect(w.get('[data-testid="rule-save"]').attributes("disabled")).toBeUndefined());
    await w.get('[data-testid="rule-save"]').trigger("click");
    const [rule, events] = saves.at(-1) as [{ map: string; filter: string }, string[]];
    expect(rule.map).toContain("env.studioKey");
    expect(JSON.stringify(saves)).not.toContain("s3cr3t-value");
    expect(events).toEqual(["message.sent", "message.received"]);
    w.unmount();
  });
});

describe("RuleListsPage", () => {
  it("searches, sorts and pages on the server", async () => {
    vi.mocked(listRuleLists).mockResolvedValue({
      data: [{ id: "rls_1", organizationId: "org_1", name: "vip", kind: "domains", entryCount: 3, createdAt: "2026-10-01T00:00:00Z", updatedAt: "2026-10-02T00:00:00Z", version: 1 }],
      total: 41,
    });
    const Page = (await import("@/features/rules/pages/RuleListsPage.vue")).default;
    const TableCard = (await import("@/components/tables/TableCard.vue")).default;
    const w = mount(Page, { global: global() });
    await flushPromises();
    expect(listRuleLists).toHaveBeenLastCalledWith({ page: 1, limit: 25, q: undefined, sort: "name" });
    const table = w.findComponent(TableCard);
    table.vm.$emit("update:search", "vip");
    await flushPromises();
    expect(listRuleLists).toHaveBeenLastCalledWith({ page: 1, limit: 25, q: "vip", sort: "name" });
    table.vm.$emit("update:sortBy", [{ key: "entryCount", order: "desc" }]);
    await flushPromises();
    expect(listRuleLists).toHaveBeenLastCalledWith({ page: 1, limit: 25, q: "vip", sort: "-entryCount" });
    table.vm.$emit("update:page", 2);
    await flushPromises();
    expect(listRuleLists).toHaveBeenLastCalledWith({ page: 2, limit: 25, q: "vip", sort: "-entryCount" });
    expect(w.text()).toContain("vip");
    w.unmount();
  });
});
