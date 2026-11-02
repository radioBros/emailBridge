<script setup lang="ts">
/** How the saved rule behaves in production: counters and the most recent decisions. */
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiHistory, mdiRefresh } from "@mdi/js";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import {
  getRuleStats,
  listRuleDecisions,
  type RuleDecision,
  type RuleStats,
  type RuleTargetKind,
} from "@/api/rules";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import DateUtil from "@/helpers/Date";
import { useNotifyStore } from "@/stores/notify";

const props = defineProps<{
  kind: RuleTargetKind;
  targetId: string;
  /** Bumped by the editor after a save, to reload. */
  revision: number;
}>();

const emit = defineEmits<{
  versions: [];
  inspect: [eventId: string];
}>();

const { t, locale } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const load = useLoadState();
const stats = ref<RuleStats | null>(null);
const decisions = ref<Array<RuleDecision & { id: string }>>([]);
const page = ref(1);
const ipp = ref(10);

const fetch = async (): Promise<void> => {
  if (!props.targetId) return;
  load.start();
  try {
    const [s, d] = await Promise.all([
      getRuleStats(props.kind, props.targetId),
      listRuleDecisions(props.kind, props.targetId, 50),
    ]);
    stats.value = s;
    decisions.value = (d.data ?? []).map((x, i) => ({ ...x, id: `${x.eventId}-${i}` }));
    load.finish(true);
  } catch (e) {
    load.finish(false);
    notify.push({ message: messageOf(e), color: "error" });
  }
};

onMounted(fetch);
watch(() => props.revision, fetch);

const relative = (iso?: string): string =>
  iso ? (DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso)) : t("rules.insights.never");

const matchRate = computed((): string => {
  const s = stats.value;
  if (!s?.evaluated) return "–";
  return `${Math.round((s.matched / s.evaluated) * 1000) / 10}%`;
});

const tiles = computed(() => [
  { key: "evaluated", value: stats.value?.evaluated ?? 0, color: "primary" },
  { key: "matched", value: stats.value?.matched ?? 0, color: "success" },
  { key: "skipped", value: stats.value?.skipped ?? 0, color: "secondary" },
  { key: "errors", value: stats.value?.errors ?? 0, color: stats.value?.errors ? "error" : "secondary" },
]);

const headers = computed((): ServerTableHeader[] => [
  { key: "at", title: t("rules.insights.col_at"), sortable: false, forced: true },
  { key: "matched", title: t("rules.insights.col_result"), sortable: false },
  { key: "reason", title: t("rules.insights.col_reason"), sortable: false },
  { key: "eventId", title: t("rules.insights.col_event"), sortable: false },
]);

// The server keeps the last 50 decisions per rule (W73 §5); the table pages through that capped set.
const pageRows = computed(() => decisions.value.slice((page.value - 1) * ipp.value, page.value * ipp.value));

const fmt = (n: number): string => n.toLocaleString(locale.value);
</script>

<template>
  <div class="d-flex flex-column ga-4">
    <LoadingSkeleton
      v-if="load.isFirstLoad.value"
      shape="kpi"
    />
    <div
      v-else
      class="position-relative d-flex flex-column ga-3"
    >
      <AppSectionLoader :status="load.isRefreshing.value" />
      <v-row class="ma-n1">
        <v-col
          v-for="tile in tiles"
          :key="tile.key"
          cols="6"
          md="3"
          class="pa-1"
        >
          <div
            :data-testid="`rule-stat-${tile.key}`"
            class="stat pa-3"
          >
            <div class="text-caption text-medium-emphasis">
              {{ t(`rules.insights.${tile.key}`) }}
            </div>
            <div
              :class="`text-${tile.color}`"
              class="text-headline-small font-weight-bold stat-number"
            >
              {{ fmt(tile.value) }}
            </div>
          </div>
        </v-col>
      </v-row>
      <div class="d-flex flex-wrap align-center gc-4 gr-1 text-body-2">
        <span>{{ t("rules.insights.match_rate", { rate: matchRate }) }}</span>
        <span class="text-medium-emphasis">{{ t("rules.insights.last_match", { when: relative(stats?.lastMatchedAt) }) }}</span>
        <span
          v-if="stats?.lastError"
          class="text-error"
        >
          {{ t("rules.insights.last_error", { when: relative(stats?.lastErrorAt), error: stats.lastError }) }}
        </span>
        <v-spacer />
        <v-btn-inline
          :prepend-icon="mdiRefresh"
          color="secondary"
          @click="fetch"
        >
          {{ t("rules.insights.refresh") }}
        </v-btn-inline>
        <v-btn-inline
          :prepend-icon="mdiHistory"
          color="accent"
          data-testid="rule-open-versions"
          @click="emit('versions')"
        >
          {{ t("rules.insights.versions") }}
        </v-btn-inline>
      </div>
    </div>

    <TableCard
      :headers="headers"
      :items="pageRows"
      :total="decisions.length"
      :page="page"
      :ipp="ipp"
      :sort-by="[]"
      :loading="load.isLoading.value"
      :searchable="false"
      :fill-height="false"
      :max-height="360"
      table-name="rule-decisions"
      @update:page="(p) => (page = p)"
      @update:ipp="(n) => { ipp = n; page = 1; }"
    >
      <template #no-data>
        <div class="text-medium-emphasis pa-6 text-center">
          {{ t("rules.insights.no_decisions") }}
        </div>
      </template>
      <template #[`item.at`]="{ item }">
        <span class="text-no-wrap">{{ relative(item.at) }}</span>
      </template>
      <template #[`item.matched`]="{ item }">
        <v-chip :color="item.error ? 'error' : item.matched ? 'success' : 'warning'">
          {{ item.error ? t("rules.test.verdict_error") : item.matched ? t("rules.test.verdict_match") : t("rules.test.verdict_nomatch") }}
        </v-chip>
      </template>
      <template #[`item.reason`]="{ item }">
        <code class="text-body-2 reason">{{ item.error || item.reason || "–" }}</code>
      </template>
      <template #[`item.eventId`]="{ item }">
        <v-btn-inline
          color="primary"
          class="font-mono"
          @click="emit('inspect', item.eventId)"
        >
          {{ t("rules.insights.test_with") }}
        </v-btn-inline>
      </template>
    </TableCard>
  </div>
</template>

<style scoped lang="scss">
.stat {
  border-radius: 12px;
  background: rgb(var(--v-theme-surface-variant), 0.5);
}

.stat-number {
  font-variant-numeric: tabular-nums;
}

.reason {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  font-family: "JetBrains Mono", ui-monospace, monospace;
}

.font-mono {
  font-family: "JetBrains Mono", ui-monospace, monospace;
}
</style>
