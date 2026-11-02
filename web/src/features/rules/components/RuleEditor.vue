<script setup lang="ts">
/**
 * The "Filter & transform" editor shared by webhook endpoints and routes: when to send (guided builder
 * or CEL), what to send (visual payload tree or CEL), a live test against real events, production
 * counters, decisions and versions. The page owns loading and saving; this component owns the draft.
 */
import { computed, onMounted, ref, toRef, watch } from "vue";
import { useDisplay } from "vuetify";
import { useI18n } from "vue-i18n";
import {
  mdiCodeBraces,
  mdiContentCopy,
  mdiFilterVariant,
  mdiFlaskOutline,
  mdiLightningBolt,
  mdiPackageVariantClosed,
  mdiPuzzleOutline,
  mdiTune,
  mdiUndoVariant,
  mdiDeleteOutline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import { storeEnvSecret, type EndpointRule, type RuleTargetKind } from "@/api/rules";
import { actionOf } from "@/constants/Actions";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import CodePane from "@/features/rules/components/CodePane.vue";
import FilterBuilder from "@/features/rules/components/FilterBuilder.vue";
import MapEditor from "@/features/rules/components/MapEditor.vue";
import PresetDialog from "@/features/rules/components/PresetDialog.vue";
import RuleInsights from "@/features/rules/components/RuleInsights.vue";
import RuleTestPanel from "@/features/rules/components/RuleTestPanel.vue";
import RuleVersionsDrawer from "@/features/rules/components/RuleVersionsDrawer.vue";
import { createRuleContext, provideRuleContext } from "@/features/rules/composables/useRuleContext";
import { useRuleDraft } from "@/features/rules/composables/useRuleDraft";
import { accountIdsIn } from "@/features/rules/model/builder";
import type { Preset } from "@/features/rules/model/presets";

const props = withDefaults(
  defineProps<{
    kind: RuleTargetKind;
    targetId: string;
    rule: EndpointRule | null;
    /** The endpoint's event subscription (null for routes). */
    subscribed?: string[] | null;
    /** Account the folder pickers start with (e.g. the endpoint's account filter). */
    accountHint?: string;
    saving?: boolean;
    /** Bumped by the page after a save / restore. */
    revision?: number;
    currentVersion?: number;
    /** Offer "remove rule" (endpoints: the rule is optional). */
    removable?: boolean;
  }>(),
  { subscribed: null, accountHint: "", saving: false, revision: 0, currentVersion: 0, removable: false },
);

const emit = defineEmits<{
  save: [rule: EndpointRule, events: string[] | null];
  remove: [];
  restore: [version: number];
  "update:dirty": [dirty: boolean];
}>();

const { t } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const saveAction = actionOf("save");
const { lgAndUp } = useDisplay();

const ctx = createRuleContext();
provideRuleContext(ctx);

const draft = useRuleDraft({ kind: toRef(props, "kind"), targetId: toRef(props, "targetId") });
const presetsOpen = ref(false);
const presetApplying = ref(false);
const versionsOpen = ref(false);
const filterPane = ref<InstanceType<typeof CodePane> | null>(null);
const mapPane = ref<InstanceType<typeof CodePane> | null>(null);
const extraEvents = ref<string[]>([]);

const load = async (): Promise<void> => {
  extraEvents.value = [];
  await draft.init(props.rule);
};

onMounted(() => {
  void load();
  void ctx.loadLists();
});

watch(
  () => props.revision,
  async () => {
    await load();
  },
);

watch(draft.dirty, (d) => emit("update:dirty", d || extraEvents.value.length > 0), { immediate: true });
watch(extraEvents, () => emit("update:dirty", draft.dirty.value || extraEvents.value.length > 0), { deep: true });

// Context for the pickers: the rule's own account, else the hint.
watch(
  () => [accountIdsIn(draft.builder.value), props.accountHint] as const,
  ([ids, hint]) => {
    if (!ctx.contextAccountId.value) ctx.contextAccountId.value = ids[0] || hint || "";
  },
  { immediate: true, deep: true },
);

watch(draft.ruleTypes, (types) => (ctx.eventTypes.value = types), { immediate: true });
watch(draft.secretNames, (names) => (ctx.secretNames.value = names), { immediate: true });

const listNames = computed(() => ctx.lists.value.map((l) => l.name));

/** Event types the rule needs that the endpoint does not subscribe to. */
const missingEvents = computed((): string[] => {
  const sub = props.subscribed;
  if (!sub || sub.includes("*")) return [];
  const have = new Set([...sub, ...extraEvents.value]);
  return draft.ruleTypes.value.filter((ty) => !have.has(ty));
});

const subscribeMissing = (): void => {
  extraEvents.value = [...new Set([...extraEvents.value, ...missingEvents.value])];
};

const dirtyAll = computed(() => draft.dirty.value || extraEvents.value.length > 0);

const save = (): void => {
  const events = extraEvents.value.length && props.subscribed ? [...new Set([...props.subscribed, ...extraEvents.value])] : null;
  emit("save", draft.payload(), events);
};

const discard = async (): Promise<void> => {
  await load();
};

const onPreset = async (p: { preset: Preset; secretName: string; secretValue: string }): Promise<void> => {
  const r = p.preset.apply(p.secretName);
  if (p.secretValue) {
    presetApplying.value = true;
    try {
      await storeEnvSecret(p.secretName, p.secretValue);
      notify.push({ message: t("rules.preset.secret_stored", { name: p.secretName }), color: "success" });
    } catch (e) {
      notify.push({ message: messageOf(e), color: "error" });
      presetApplying.value = false;
      return;
    }
    presetApplying.value = false;
  }
  draft.applyPreset(r);
  if (r.events && props.subscribed && !props.subscribed.includes("*")) {
    extraEvents.value = [...new Set([...extraEvents.value, ...r.events.filter((e) => !props.subscribed!.includes(e))])];
  }
  presetsOpen.value = false;
  notify.push({ message: t("rules.preset.applied", { name: t(`rules.preset.${p.preset.id}.title`) }), color: "success" });
};

const filterModeModel = computed({
  get: () => draft.filterMode.value,
  set: async (m) => {
    if (m === draft.filterMode.value) return;
    if (m === "code") draft.toCode();
    else await draft.toBuilder();
  },
});

const mapModeModel = computed({
  get: () => draft.mapMode.value,
  set: (m) => {
    if (m === draft.mapMode.value) return;
    if (m === "code") draft.mapToCode();
    else draft.mapToVisual();
  },
});

const copied = ref(false);
const copyCel = async (): Promise<void> => {
  try {
    await navigator.clipboard.writeText(draft.compiled.value);
    copied.value = true;
    setTimeout(() => (copied.value = false), 1400);
  } catch {
    copied.value = false;
  }
};

const reveal = (path: "filter" | "map", line: number, column: number): void => {
  if (path === "filter") {
    if (draft.filterMode.value !== "code") draft.toCode();
    setTimeout(() => filterPane.value?.reveal(line, column), 60);
  } else {
    if (draft.mapMode.value !== "code") draft.mapToCode();
    setTimeout(() => mapPane.value?.reveal(line, column), 60);
  }
};

const inspect = (eventId: string): void => {
  draft.source.value = { mode: "recent", eventId };
};

const hasMap = computed(() => draft.effectiveMap.value !== "");
const savedRule = computed(() => Boolean(props.rule && (props.rule.filter || props.rule.map)));

defineExpose({ save, dirty: dirtyAll });
</script>

<template>
  <div class="d-flex flex-column ga-4">
    <div class="d-flex flex-wrap align-center ga-2">
      <slot name="title" />
      <v-spacer />
      <v-btn
        :prepend-icon="mdiPuzzleOutline"
        color="accent"
        data-testid="rule-presets"
        @click="presetsOpen = true"
      >
        {{ t("rules.presets") }}
      </v-btn>
      <v-btn
        v-if="removable && savedRule"
        :prepend-icon="mdiDeleteOutline"
        color="error"
        data-testid="rule-remove"
        @click="emit('remove')"
      >
        {{ t("rules.remove_rule") }}
      </v-btn>
      <v-btn
        :disabled="!dirtyAll || saving"
        :prepend-icon="mdiUndoVariant"
        color="warning"
        @click="discard"
      >
        {{ t("rules.discard") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :disabled="!dirtyAll || draft.compiling.value"
        :loading="saving"
        data-testid="rule-save"
        @click="save"
      >
        {{ t("rules.save") }}
      </v-btn>
    </div>

    <v-alert
      v-if="missingEvents.length"
      type="warning"
      data-testid="rule-missing-events"
    >
      {{ t("rules.missing_events", { types: missingEvents.join(", ") }) }}
      <template #append>
        <v-btn
          color="warning"
          @click="subscribeMissing"
        >
          {{ t("rules.subscribe") }}
        </v-btn>
      </template>
    </v-alert>
    <v-alert
      v-if="extraEvents.length"
      type="info"
    >
      {{ t("rules.will_subscribe", { types: extraEvents.join(", ") }) }}
    </v-alert>

    <v-row>
      <v-col
        cols="12"
        lg="7"
        class="d-flex flex-column ga-4"
      >
        <AppCard
          class="rule-card h-auto"
          data-testid="rule-filter-card"
        >
          <template #header>
            <div class="d-flex flex-wrap align-center ga-2 w-100">
              <v-avatar color="primary">
                <v-icon :icon="mdiFilterVariant" />
              </v-avatar>
              <div class="min-w-0 flex-grow-1">
                <div class="text-title-medium font-weight-bold">
                  {{ t("rules.filter_title") }}
                </div>
                <div class="text-body-2 text-medium-emphasis">
                  {{ t("rules.filter_subtitle") }}
                </div>
              </div>
              <v-btn-toggle
                v-model="filterModeModel"
                :aria-label="t('rules.mode')"
                color="primary"
                data-testid="rule-filter-mode"
                mandatory
                divided
              >
                <v-btn
                  :prepend-icon="mdiTune"
                  value="builder"
                >
                  {{ t("rules.mode_builder") }}
                </v-btn>
                <v-btn
                  :prepend-icon="mdiCodeBraces"
                  value="code"
                  data-testid="rule-filter-code"
                >
                  {{ t("rules.mode_code") }}
                </v-btn>
              </v-btn-toggle>
            </div>
          </template>

          <div class="d-flex flex-column ga-3">
            <v-alert
              v-if="draft.filterMode.value === 'code' && draft.filterReason.value"
              type="info"
            >
              {{ t(`rules.code_reason.${draft.filterReason.value}`) }}
              <template
                v-if="draft.filterReason.value !== 'comment'"
                #append
              >
                <v-btn
                  color="info"
                  @click="draft.discardCodeToBuilder()"
                >
                  {{ t("rules.back_to_builder") }}
                </v-btn>
              </template>
            </v-alert>

            <FilterBuilder
              v-if="draft.filterMode.value === 'builder'"
              :group="draft.builder.value"
              :event-types="draft.ruleTypes.value"
              @change="draft.onBuilderChange"
            />
            <CodePane
              v-else
              ref="filterPane"
              v-model="draft.filterCode.value"
              :errors="draft.filterErrors.value"
              :event-types="draft.ruleTypes.value"
              :secret-names="draft.secretNames.value"
              :list-names="listNames"
              target="filter"
            />

            <div
              v-if="draft.filterMode.value === 'builder'"
              class="cel-preview d-flex align-start ga-2 pa-3"
              data-testid="rule-cel-preview"
            >
              <div class="flex-grow-1 min-w-0">
                <div class="text-caption text-medium-emphasis mb-1 d-flex align-center ga-2">
                  {{ t("rules.generated_cel") }}
                  <v-progress-circular
                    v-if="draft.compiling.value"
                    :size="12"
                    width="2"
                    indeterminate
                  />
                </div>
                <code
                  v-if="draft.compileError.value"
                  class="text-error cel-text"
                >{{ draft.compileError.value }}</code>
                <code
                  v-else
                  class="cel-text"
                >{{ draft.compiled.value || t("rules.matches_everything") }}</code>
              </div>
              <v-btn-affix
                :icon="mdiContentCopy"
                :color="copied ? 'success' : undefined"
                :disabled="!draft.compiled.value"
                :aria-label="t('rules.copy')"
                @click="copyCel"
              />
            </div>
          </div>
        </AppCard>

        <AppCard
          class="rule-card h-auto"
          data-testid="rule-map-card"
        >
          <template #header>
            <div class="d-flex flex-wrap align-center ga-2 w-100">
              <v-avatar color="secondary">
                <v-icon :icon="mdiPackageVariantClosed" />
              </v-avatar>
              <div class="min-w-0 flex-grow-1">
                <div class="text-title-medium font-weight-bold">
                  {{ t("rules.map_title") }}
                </div>
                <div class="text-body-2 text-medium-emphasis">
                  {{ t("rules.map_subtitle") }}
                </div>
              </div>
              <v-btn-toggle
                v-model="mapModeModel"
                :aria-label="t('rules.mode')"
                color="secondary"
                data-testid="rule-map-mode"
                mandatory
                divided
              >
                <v-btn
                  :prepend-icon="mdiTune"
                  value="visual"
                >
                  {{ t("rules.mode_visual") }}
                </v-btn>
                <v-btn
                  :prepend-icon="mdiCodeBraces"
                  value="code"
                >
                  {{ t("rules.mode_code") }}
                </v-btn>
              </v-btn-toggle>
            </div>
          </template>

          <div class="d-flex flex-column ga-3">
            <v-alert
              v-if="draft.mapMode.value === 'code' && draft.mapReason.value"
              type="info"
            >
              {{ t(`rules.code_reason.${draft.mapReason.value}`) }}
            </v-alert>
            <MapEditor
              v-if="draft.mapMode.value === 'visual'"
              :root="draft.mapRoot.value"
              :event-types="draft.ruleTypes.value"
              :secret-names="draft.secretNames.value"
              @update:root="draft.setMapRoot"
              @change="draft.onMapChange"
            />
            <CodePane
              v-else
              ref="mapPane"
              v-model="draft.mapCode.value"
              :errors="draft.mapErrors.value"
              :event-types="draft.ruleTypes.value"
              :secret-names="draft.secretNames.value"
              :list-names="listNames"
              target="map"
            />
            <v-alert
              v-if="draft.needs.value.length"
              :icon="mdiLightningBolt"
              type="info"
              data-testid="rule-needs"
            >
              {{ t("rules.needs", { what: draft.needs.value.map((n) => t(`rules.need.${n}`)).join(", ") }) }}
            </v-alert>
          </div>
        </AppCard>
      </v-col>

      <v-col
        cols="12"
        lg="5"
      >
        <AppCard
          :class="{ 'test-card--sticky': lgAndUp }"
          class="rule-card"
          data-testid="rule-test-card"
        >
          <template #header>
            <div class="d-flex align-center ga-2 w-100">
              <v-avatar color="info">
                <v-icon :icon="mdiFlaskOutline" />
              </v-avatar>
              <div class="min-w-0">
                <div class="text-title-medium font-weight-bold">
                  {{ t("rules.test.title") }}
                </div>
                <div class="text-body-2 text-medium-emphasis">
                  {{ t("rules.test.subtitle") }}
                </div>
              </div>
            </div>
          </template>
          <RuleTestPanel
            v-model:source="draft.source.value"
            :result="draft.result.value"
            :failure="draft.failure.value"
            :first-load="draft.testFirstLoad.value"
            :refreshing="draft.testRefreshing.value"
            :has-map="hasMap"
            :rule-types="draft.ruleTypes.value"
            @reveal="reveal"
            @retry="draft.runTest()"
          />
        </AppCard>
      </v-col>
    </v-row>
    <AppCard
      v-if="savedRule"
      class="rule-card"
      data-testid="rule-insights-card"
    >
      <template #header>
        <div class="text-title-medium font-weight-bold">
          {{ t("rules.insights.title") }}
        </div>
      </template>
      <RuleInsights
        :kind="kind"
        :target-id="targetId"
        :revision="revision"
        @versions="versionsOpen = true"
        @inspect="inspect"
      />
    </AppCard>

    <PresetDialog
      v-model="presetsOpen"
      :applying="presetApplying"
      @apply="onPreset"
    />
    <RuleVersionsDrawer
      v-model="versionsOpen"
      :kind="kind"
      :target-id="targetId"
      :current-version="currentVersion"
      :current-filter="rule?.filter ?? ''"
      :current-map="rule?.map ?? ''"
      :restoring="saving"
      @restore="(v) => emit('restore', v)"
    />
  </div>
</template>

<style scoped lang="scss">
.cel-preview {
  border-radius: 12px;
  background: rgb(var(--v-theme-primary), 0.05);
  border: 1px dashed rgb(var(--v-theme-primary), 0.3);
}

.cel-text {
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.8125rem;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

// AppCard fills its parent by default (height: 100%); the editor's cards follow their content.
.rule-card {
  height: auto;
  flex-shrink: 0;
}

.test-card--sticky {
  position: sticky;
  top: 80px;
}
</style>
