<script setup lang="ts">
/**
 * Test panel: what to test against (a recent real event of the organisation, pasted JSON, or a
 * generated sample) and the live answer — MATCH / NO MATCH, why (each top-level term), the mapped
 * body, cost and time. The editor runs the evaluation; this panel owns the source picker.
 */
import { computed, onMounted, ref, watch } from "vue";
import { useDisplay } from "vuetify";
import { useI18n } from "vue-i18n";
import {
  mdiAlertCircleOutline,
  mdiCheckCircle,
  mdiCloseCircle,
  mdiCrosshairsGps,
  mdiFlaskOutline,
  mdiHistory,
  mdiRefresh,
  mdiClipboardTextOutline,
} from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { listTestEvents, type RuleTestEvent, type RuleTestResult } from "@/api/rules";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import DateUtil from "@/helpers/Date";
import { useNotifyStore } from "@/stores/notify";
import { EVENT_TYPES, eventTypeKey } from "@/features/rules/catalog/catalog";
import AccountSelect from "@/features/rules/components/AccountSelect.vue";
import CodeEditor from "@/features/rules/components/CodeEditor.vue";
import JsonView from "@/features/rules/components/JsonView.vue";
import { useRuleContext } from "@/features/rules/composables/useRuleContext";
import type { TestSource } from "@/features/rules/model/testSource";

const props = defineProps<{
  source: TestSource;
  result: RuleTestResult | null;
  /** Request failure (not a rule error, which is in result.error). */
  failure: string | null;
  firstLoad: boolean;
  refreshing: boolean;
  /** The rule has a map (else the body is the normal payload). */
  hasMap: boolean;
  /** Event types the rule filters on, to preselect the recent-events filter. */
  ruleTypes: string[];
}>();

const emit = defineEmits<{
  "update:source": [source: TestSource];
  reveal: [path: "filter" | "map", line: number, column: number];
  retry: [];
}>();

const { t, te, locale } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const ctx = useRuleContext();
const { xs } = useDisplay();

const events = ref<RuleTestEvent[]>([]);
const typeFilter = ref<string>(props.ruleTypes[0] ?? "");
const accountFilter = ref<string>("");
const pasteDraft = ref(props.source.mode === "paste" ? props.source.json : "");
const sampleType = ref(props.source.mode === "sample" ? props.source.type : (props.ruleTypes[0] ?? "message.received"));
const load = useLoadState();

const mode = computed({
  get: () => props.source.mode,
  set: (m: TestSource["mode"]) => {
    if (m === "recent") emit("update:source", { mode: "recent", eventId: events.value[0]?.id ?? "" });
    else if (m === "paste") emit("update:source", { mode: "paste", json: pasteDraft.value });
    else emit("update:source", { mode: "sample", type: sampleType.value });
  },
});

const loadEvents = async (): Promise<void> => {
  load.start();
  try {
    const res = await listTestEvents({
      type: typeFilter.value || undefined,
      accountId: accountFilter.value || undefined,
      limit: 30,
    });
    events.value = res.data ?? [];
    for (const e of events.value) if (e.accountId) void ctx.ensureAccount(e.accountId);
    load.finish(true);
    if (props.source.mode === "recent" && !events.value.some((e) => e.id === (props.source as { eventId: string }).eventId)) {
      emit("update:source", { mode: "recent", eventId: events.value[0]?.id ?? "" });
    }
  } catch (e) {
    load.finish(false);
    notify.push({ message: messageOf(e), color: "error" });
  }
};

onMounted(() => {
  void loadEvents();
});
watch([typeFilter, accountFilter], () => void loadEvents());

const typeItems = computed(() => [
  { title: t("rules.test.all_types"), value: "" },
  ...EVENT_TYPES.map((e) => ({ title: e, value: e, subtitle: te(eventTypeKey(e)) ? t(eventTypeKey(e)) : "" })),
]);

const pick = (e: RuleTestEvent): void => emit("update:source", { mode: "recent", eventId: e.id });

const pasteError = computed((): string | null => {
  if (!pasteDraft.value.trim()) return null;
  try {
    const v = JSON.parse(pasteDraft.value) as unknown;
    return v && typeof v === "object" && !Array.isArray(v) ? null : t("rules.test.paste_not_object");
  } catch (e) {
    return (e as Error).message;
  }
});

watch(pasteDraft, (json) => {
  if (props.source.mode === "paste" && !pasteError.value) emit("update:source", { mode: "paste", json });
});

watch(sampleType, (type) => {
  if (props.source.mode === "sample" && type) emit("update:source", { mode: "sample", type });
});

const relative = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const verdict = computed((): "match" | "nomatch" | "error" | null => {
  if (!props.result) return null;
  if (props.result.error) return "error";
  return props.result.matched ? "match" : "nomatch";
});

const verdictColor = computed(() =>
  verdict.value === "match" ? "success" : verdict.value === "error" ? "error" : "warning",
);

const verdictIcon = computed(() =>
  verdict.value === "match" ? mdiCheckCircle : verdict.value === "error" ? mdiAlertCircleOutline : mdiCloseCircle,
);

const trace = computed(() => props.result?.trace ?? []);
const failedTerms = computed(() => trace.value.filter((x) => !x.result).length);

const accountName = (id?: string): string => (id ? ctx.accountLabel(id) : "");
</script>

<template>
  <div
    class="d-flex flex-column ga-4"
    data-testid="rule-test-panel"
  >
    <v-btn-toggle
      v-model="mode"
      :aria-label="t('rules.test.source')"
      color="primary"
      class="w-100"
      mandatory
      divided
    >
      <v-btn
        :prepend-icon="xs ? undefined : mdiHistory"
        value="recent"
        class="flex-grow-1"
        data-testid="test-source-recent"
      >
        {{ t("rules.test.recent") }}
      </v-btn>
      <v-btn
        :prepend-icon="xs ? undefined : mdiClipboardTextOutline"
        value="paste"
        class="flex-grow-1"
        data-testid="test-source-paste"
      >
        {{ t("rules.test.paste") }}
      </v-btn>
      <v-btn
        :prepend-icon="xs ? undefined : mdiFlaskOutline"
        value="sample"
        class="flex-grow-1"
        data-testid="test-source-sample"
      >
        {{ t("rules.test.sample") }}
      </v-btn>
    </v-btn-toggle>

    <div
      v-if="source.mode === 'recent'"
      class="d-flex flex-column ga-2"
    >
      <v-row class="ma-n1">
        <v-col
          cols="12"
          sm="6"
          class="pa-1"
        >
          <AutocompleteSearch
            v-model="typeFilter"
            :items="typeItems"
            :label="t('rules.test.filter_type')"
            :search-fields="['subtitle']"
            class="no-validation"
            hide-details
          />
        </v-col>
        <v-col
          cols="12"
          sm="6"
          class="pa-1"
        >
          <AccountSelect
            v-model="accountFilter"
            :label="t('rules.test.filter_account')"
          />
        </v-col>
      </v-row>
      <LoadingSkeleton
        v-if="load.isFirstLoad.value"
        :rows="4"
        shape="list"
      />
      <div
        v-else
        class="position-relative"
      >
        <AppSectionLoader :status="load.isRefreshing.value" />
        <div
          v-if="!events.length"
          class="text-body-2 text-medium-emphasis pa-3 test-empty"
        >
          {{ t("rules.test.no_events") }}
        </div>
        <v-list
          v-else
          :aria-label="t('rules.test.recent')"
          class="test-events py-0"
          data-testid="test-events"
        >
          <v-list-item
            v-for="e in events"
            :key="e.id"
            :active="source.mode === 'recent' && source.eventId === e.id"
            :data-testid="`test-event-${e.id}`"
            color="primary"
            @click="pick(e)"
          >
            <v-list-item-title class="text-body-2 font-weight-medium">
              {{ e.subject || e.type }}
            </v-list-item-title>
            <v-list-item-subtitle class="d-flex flex-wrap ga-1 align-center">
              <span class="font-mono">{{ e.type }}</span>
              <span v-if="e.accountId">· {{ accountName(e.accountId) }}</span>
              <span>· {{ relative(e.time) }}</span>
            </v-list-item-subtitle>
          </v-list-item>
        </v-list>
        <div class="d-flex justify-end mt-1">
          <v-btn-inline
            :prepend-icon="mdiRefresh"
            color="secondary"
            @click="loadEvents"
          >
            {{ t("rules.test.reload") }}
          </v-btn-inline>
        </div>
      </div>
    </div>

    <div
      v-else-if="source.mode === 'paste'"
      class="d-flex flex-column ga-2"
    >
      <CodeEditor
        v-model="pasteDraft"
        :aria-label="t('rules.test.paste_label')"
        :placeholder="t('rules.test.paste_placeholder')"
        language="json"
        min-height="160px"
      />
      <div
        v-if="pasteError"
        class="text-caption text-error"
        role="alert"
      >
        {{ pasteError }}
      </div>
    </div>

    <AutocompleteSearch
      v-else
      v-model="sampleType"
      :items="typeItems.slice(1)"
      :label="t('rules.test.sample_type')"
      :search-fields="['subtitle']"
      class="no-validation"
      data-testid="test-sample-type"
      hide-details
    />

    <v-divider />

    <div aria-live="polite">
      <LoadingSkeleton
        v-if="firstLoad"
        shape="card"
      />
      <div
        v-else-if="failure"
        class="d-flex align-center ga-3"
      >
        <v-avatar-small color="error">
          <v-icon :icon="mdiAlertCircleOutline" />
        </v-avatar-small>
        <span class="text-body-2 flex-grow-1">{{ failure }}</span>
        <v-btn-inline
          :prepend-icon="mdiRefresh"
          color="primary"
          @click="emit('retry')"
        >
          {{ t("rules.test.retry") }}
        </v-btn-inline>
      </div>
      <div
        v-else-if="!result"
        class="text-body-2 text-medium-emphasis"
      >
        {{ t("rules.test.pick_source") }}
      </div>
      <div
        v-else
        class="position-relative d-flex flex-column ga-3"
      >
        <AppSectionLoader :status="refreshing" />
        <div
          :class="`verdict--${verdict}`"
          class="verdict d-flex align-center ga-3 pa-3"
          data-testid="test-verdict"
        >
          <v-icon-status
            :icon="verdictIcon"
            :color="verdictColor"
          />
          <div class="flex-grow-1 min-w-0">
            <div
              :class="`text-${verdictColor}`"
              class="text-title-large font-weight-bold verdict-word"
            >
              {{ t(`rules.test.verdict_${verdict}`) }}
            </div>
            <div class="text-caption text-medium-emphasis">
              {{ t("rules.test.cost", { cost: result.cost ?? result.costUsed ?? 0, ms: result.durationMs }) }}
              <template v-if="verdict === 'nomatch' && failedTerms">
                · {{ t("rules.test.failed_terms", { n: failedTerms }) }}
              </template>
            </div>
          </div>
        </div>

        <div
          v-if="result.error"
          class="d-flex flex-column ga-2"
          role="alert"
        >
          <div class="text-body-2 text-error font-mono error-text">
            {{ result.error }}
          </div>
          <div v-if="result.errorAt">
            <v-btn-inline
              :prepend-icon="mdiCrosshairsGps"
              color="error"
              data-testid="test-reveal-error"
              @click="emit('reveal', result.errorAt.path, result.errorAt.line, result.errorAt.column)"
            >
              {{ t("rules.test.show_error", { path: t(`rules.test.path_${result.errorAt.path}`), line: result.errorAt.line, column: result.errorAt.column }) }}
            </v-btn-inline>
          </div>
        </div>

        <div v-if="trace.length">
          <div class="text-label-large font-weight-bold mb-2">
            {{ t("rules.test.why") }}
          </div>
          <ul
            class="trace d-flex flex-column ga-1 pa-0 ma-0"
            data-testid="test-trace"
          >
            <li
              v-for="(term, i) in trace"
              :key="i"
              :class="term.result ? 'trace--pass' : 'trace--fail'"
              class="trace-row d-flex align-start ga-2 px-2 py-1"
            >
              <v-icon-inline
                :icon="term.result ? mdiCheckCircle : mdiCloseCircle"
                :color="term.result ? 'success' : 'error'"
                class="mt-1 flex-shrink-0"
              />
              <code class="font-mono text-body-2 trace-expr">{{ term.expr }}</code>
              <span
                :class="term.result ? 'text-success' : 'text-error'"
                class="text-caption font-weight-bold ms-auto flex-shrink-0 mt-1"
              >
                {{ term.result ? t("rules.test.term_true") : t("rules.test.term_false") }}
              </span>
            </li>
          </ul>
        </div>

        <div v-if="result.matched && !result.error">
          <div class="text-label-large font-weight-bold mb-2">
            {{ hasMap ? t("rules.test.mapped") : t("rules.test.delivered_as_is") }}
          </div>
          <JsonView
            v-if="hasMap"
            :value="result.mapped ?? null"
            data-testid="test-mapped"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.test-events {
  max-height: 264px;
  overflow-y: auto;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
}

.test-empty {
  border: 1px dashed rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
}

.verdict {
  border-radius: 14px;
  background: rgb(var(--v-theme-warning), 0.08);
}

.verdict--match {
  background: rgb(var(--v-theme-success), 0.1);
}

.verdict--error {
  background: rgb(var(--v-theme-error), 0.08);
}

.verdict-word {
  letter-spacing: 0.02em;
}

.trace {
  list-style: none;
}

.trace-row {
  border-radius: 10px;
}

.trace--fail {
  background: rgb(var(--v-theme-error), 0.06);
}

.trace-expr {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.font-mono {
  font-family: "JetBrains Mono", ui-monospace, monospace;
}

.error-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
</style>
