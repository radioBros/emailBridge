import { computed, ref, watch, type Ref } from "vue";
import { expressibleBuilder } from "@/api/routes";
import {
  compileRuleBuilder,
  testRule,
  type EndpointRule,
  type RuleGroup,
  type RuleTargetKind,
  type RuleTestResult,
} from "@/api/rules";
import { ApiProblemError } from "@/api/client";
import { useLoadState } from "@/composables/useLoadState";
import {
  cloneTree,
  eventTypesIn,
  newGroup,
  normalizeForCompile,
} from "@/features/rules/model/builder";
import { formatCel, payloadNeeds, positionInMessage, type CodeError } from "@/features/rules/model/code";
import { isVisual, parseMap, secretsIn, toCel, type MapNode } from "@/features/rules/model/mapTree";
import type { PresetResult } from "@/features/rules/model/presets";
import { sourceBody, type TestSource } from "@/features/rules/model/testSource";

export type FilterMode = "builder" | "code";
export type MapMode = "visual" | "code";

/** Why a filter or map opened in code mode. */
export type CodeReason = "advanced" | "edited" | "comment" | "expression" | null;

const problemText = (e: unknown): string => {
  if (e instanceof ApiProblemError) return e.problem.detail || e.problem.title || e.problem.code;
  return e instanceof Error ? e.message : String(e);
};

const isBuilder = (v: unknown): v is RuleGroup =>
  !!v && typeof v === "object" && Array.isArray((v as RuleGroup).conditions);

/**
 * The editor's draft: filter (builder tree or code) + map (tree or code), the server-compiled CEL, and
 * the live evaluation against the test source. Everything the template shows is derived here, so the
 * behaviour is tested without mounting the page.
 */
export const useRuleDraft = (opts: {
  kind: Ref<RuleTargetKind>;
  targetId: Ref<string>;
  debounceMs?: number;
}) => {
  const wait = opts.debounceMs ?? 350;

  const filterMode = ref<FilterMode>("builder");
  const filterReason = ref<CodeReason>(null);
  const builder = ref<RuleGroup>(newGroup("all", []));
  const filterCode = ref("");
  const compiled = ref("");
  const compiling = ref(false);
  const compileError = ref<string | null>(null);

  const mapMode = ref<MapMode>("visual");
  const mapReason = ref<CodeReason>(null);
  const mapRoot = ref<MapNode | null>(null);
  const mapCode = ref("");

  /** Saved state, to know what changed. */
  const saved = ref({ filter: "", map: "" });
  const tick = ref(0);

  const effectiveFilter = computed((): string => (filterMode.value === "builder" ? compiled.value : filterCode.value.trim()));
  const effectiveMap = computed((): string => {
    void tick.value; // in-place tree edits
    return mapMode.value === "visual" ? toCel(mapRoot.value) : mapCode.value.trim();
  });

  const dirty = computed(
    () => effectiveFilter.value !== saved.value.filter || effectiveMap.value !== saved.value.map,
  );

  // ---- compile the builder on the server ------------------------------------------------

  let compileTimer: ReturnType<typeof setTimeout> | undefined;
  let compileSeq = 0;

  const compileNow = async (): Promise<void> => {
    const seq = ++compileSeq;
    const tree = normalizeForCompile(builder.value);
    if (!tree.conditions.length) {
      compiled.value = "";
      compileError.value = null;
      compiling.value = false;
      return;
    }
    compiling.value = true;
    try {
      const res = await compileRuleBuilder(tree);
      if (seq !== compileSeq) return;
      compiled.value = res.ok ? res.cel : compiled.value;
      compileError.value = res.ok ? null : (res.error ?? null);
    } catch (e) {
      if (seq !== compileSeq) return;
      compileError.value = problemText(e);
    } finally {
      if (seq === compileSeq) compiling.value = false;
    }
  };

  const scheduleCompile = (): void => {
    compiling.value = true;
    clearTimeout(compileTimer);
    compileTimer = setTimeout(() => void compileNow(), wait / 2);
  };

  const onBuilderChange = (): void => {
    tick.value += 1;
    scheduleCompile();
  };

  const onMapChange = (): void => {
    tick.value += 1;
  };

  // ---- load a saved rule ----------------------------------------------------------------

  const init = async (rule: EndpointRule | null | undefined): Promise<void> => {
    const filter = (rule?.filter ?? "").trim();
    const map = (rule?.map ?? "").trim();
    filterReason.value = null;
    mapReason.value = null;
    compileError.value = null;
    filterCode.value = filter === "true" ? "" : filter;
    builder.value = newGroup("all", []);
    compiled.value = "";
    filterMode.value = "builder";

    if (isBuilder(rule?.builder)) {
      builder.value = cloneTree(rule.builder);
      await compileNow();
      if (filter && filter !== "true" && compiled.value !== filter) {
        filterMode.value = "code";
        filterReason.value = "edited";
      }
    } else if (filter && filter !== "true") {
      try {
        const exp = await expressibleBuilder(filter);
        if (exp.expressible && exp.builder) {
          builder.value = cloneTree(exp.builder as unknown as RuleGroup);
          await compileNow();
          if (compiled.value !== filter) throw new Error("not exact");
        } else throw new Error("not expressible");
      } catch {
        builder.value = newGroup("all", []);
        compiled.value = "";
        filterMode.value = "code";
        filterReason.value = "advanced";
      }
    }

    const parsed = parseMap(map);
    if (parsed.ok && isVisual(parsed.node)) {
      mapRoot.value = parsed.node;
      mapMode.value = "visual";
      mapCode.value = map;
    } else {
      mapRoot.value = null;
      mapMode.value = "code";
      mapCode.value = map;
      mapReason.value = parsed.ok ? "expression" : parsed.reason === "comment" ? "comment" : "advanced";
    }
    saved.value = { filter: effectiveFilter.value, map: effectiveMap.value };
    tick.value += 1;
  };

  /** After a save, the editor content is the new baseline. */
  const markSaved = (): void => {
    saved.value = { filter: effectiveFilter.value, map: effectiveMap.value };
  };

  // ---- mode switches --------------------------------------------------------------------

  const toCode = (): void => {
    filterCode.value = formatCel(compiled.value, "filter");
    filterMode.value = "code";
    filterReason.value = null;
  };

  /** Back to the builder: exact when the code still is what the builder produced, else via /expressible. */
  const toBuilder = async (): Promise<boolean> => {
    const code = filterCode.value.trim();
    if (!code || code === "true") {
      filterMode.value = "builder";
      filterReason.value = null;
      await compileNow();
      return true;
    }
    const sameAsBuilder = formatCel(compiled.value, "filter") === formatCel(code, "filter");
    if (sameAsBuilder && compiled.value) {
      filterMode.value = "builder";
      filterReason.value = null;
      return true;
    }
    try {
      const exp = await expressibleBuilder(code.replace(/\s*\n\s*/g, " "));
      if (exp.expressible && exp.builder) {
        builder.value = cloneTree(exp.builder as unknown as RuleGroup);
        filterMode.value = "builder";
        filterReason.value = null;
        await compileNow();
        return true;
      }
    } catch {
      // fall through
    }
    filterReason.value = "advanced";
    return false;
  };

  /** Drops the code and returns to the last builder state. */
  const discardCodeToBuilder = async (): Promise<void> => {
    filterMode.value = "builder";
    filterReason.value = null;
    await compileNow();
  };

  const mapToCode = (): void => {
    mapCode.value = toCel(mapRoot.value);
    mapMode.value = "code";
    mapReason.value = null;
  };

  const mapToVisual = (): boolean => {
    const parsed = parseMap(mapCode.value);
    if (parsed.ok && isVisual(parsed.node)) {
      mapRoot.value = parsed.node;
      mapMode.value = "visual";
      mapReason.value = null;
      tick.value += 1;
      return true;
    }
    mapReason.value = parsed.ok ? "expression" : parsed.reason === "comment" ? "comment" : "advanced";
    return false;
  };

  const setMapRoot = (node: MapNode | null): void => {
    mapRoot.value = node;
    tick.value += 1;
  };

  const applyPreset = (r: PresetResult): void => {
    if (r.builder) {
      builder.value = cloneTree(r.builder);
      filterMode.value = "builder";
      filterReason.value = null;
      scheduleCompile();
    }
    if (r.map !== undefined) {
      mapRoot.value = r.map ? cloneTree(r.map) : null;
      mapMode.value = "visual";
      mapReason.value = null;
    }
    tick.value += 1;
  };

  // ---- derived facts --------------------------------------------------------------------

  /** Event types the rule filters on (builder tree, or `type == "…"` / `type in [...]` in code). */
  const ruleTypes = computed((): string[] => {
    void tick.value;
    if (filterMode.value === "builder") return eventTypesIn(builder.value);
    const code = filterCode.value;
    const out = new Set<string>();
    for (const m of code.matchAll(/\btype\s*==\s*"([\w.]+)"/g)) out.add(m[1]!);
    for (const m of code.matchAll(/\btype\s+in\s+\[([^\]]*)\]/g)) {
      for (const s of m[1]!.matchAll(/"([\w.]+)"/g)) out.add(s[1]!);
    }
    return [...out];
  });

  const secretNames = computed((): string[] => {
    void tick.value;
    const names = new Set<string>([...secretsIn(mapRoot.value)]);
    for (const src of [mapCode.value, filterCode.value, saved.value.map, saved.value.filter]) {
      for (const m of src.matchAll(/env\.([A-Za-z_][A-Za-z0-9_]*)/g)) names.add(m[1]!);
    }
    return [...names].sort();
  });

  const needs = computed(() => payloadNeeds(effectiveFilter.value, effectiveMap.value));

  // ---- live test ------------------------------------------------------------------------

  const source = ref<TestSource>({ mode: "recent", eventId: "" });
  const result = ref<RuleTestResult | null>(null);
  const failure = ref<string | null>(null);
  const testLoad = useLoadState();
  let testTimer: ReturnType<typeof setTimeout> | undefined;
  let testSeq = 0;

  const runTest = async (): Promise<void> => {
    const body = sourceBody(source.value);
    if (!body || !opts.targetId.value) return;
    if (filterMode.value === "builder" && compiling.value) return; // the compile watcher re-runs
    const seq = ++testSeq;
    testLoad.start();
    try {
      const res = await testRule(opts.kind.value, opts.targetId.value, {
        ...body,
        filter: effectiveFilter.value || "true",
        map: effectiveMap.value,
      });
      if (seq !== testSeq) return;
      result.value = res;
      failure.value = null;
      testLoad.finish(true);
    } catch (e) {
      if (seq !== testSeq) return;
      failure.value = problemText(e);
      testLoad.finish(false);
    }
  };

  const scheduleTest = (): void => {
    clearTimeout(testTimer);
    testTimer = setTimeout(() => void runTest(), wait);
  };

  watch([effectiveFilter, effectiveMap, source, compiling], scheduleTest, { deep: true });

  /** Inline errors for the code editors, from the evaluation's error position. */
  const errorsFor = (path: "filter" | "map"): CodeError[] => {
    const r = result.value;
    if (!r?.error) return [];
    const at = r.errorAt ?? null;
    if (at) return at.path === path ? [{ line: at.line || 1, column: at.column || 1, message: r.error }] : [];
    const pos = positionInMessage(r.error);
    return pos && path === "filter" ? [{ ...pos, message: r.error }] : [];
  };

  const filterErrors = computed(() => errorsFor("filter"));
  const mapErrors = computed(() => errorsFor("map"));

  /** What the save sends. Builder mode stores the tree next to the compiled filter. */
  const payload = (): EndpointRule => {
    const out: EndpointRule = {
      filter: effectiveFilter.value,
      map: effectiveMap.value,
    };
    if (filterMode.value === "builder" && builder.value.conditions.length) {
      out.builder = normalizeForCompile(builder.value);
    }
    return out;
  };

  return {
    filterMode,
    filterReason,
    builder,
    filterCode,
    compiled,
    compiling,
    compileError,
    mapMode,
    mapReason,
    mapRoot,
    mapCode,
    effectiveFilter,
    effectiveMap,
    dirty,
    ruleTypes,
    secretNames,
    needs,
    source,
    result,
    failure,
    testFirstLoad: testLoad.isFirstLoad,
    testRefreshing: testLoad.isRefreshing,
    filterErrors,
    mapErrors,
    init,
    markSaved,
    onBuilderChange,
    onMapChange,
    toCode,
    toBuilder,
    discardCodeToBuilder,
    mapToCode,
    mapToVisual,
    setMapRoot,
    applyPreset,
    runTest,
    payload,
  };
};
