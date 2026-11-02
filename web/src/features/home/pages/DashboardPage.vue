<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiAccountGroup, mdiInbox, mdiWebhook } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import DashKpiSection from "@/features/home/components/DashKpiSection.vue";
import { useDashboardStats } from "@/features/home/composables/useDashboardStats";
import { EMPTY_VALUE } from "@/helpers/Format";

const DashStateChart = defineAsyncComponent(
  () => import("@/features/home/components/DashStateChart.vue"),
);

const { t } = useI18n();
const router = useRouter();
const {
  sandbox,
  webhooks,
  members,
  stateSegments,
  stateTotal,
  outboundSlices,
  deliverySlices,
  logLevelSlices,
  queueSlices,
  canReadLogLevels,
  canReadQueues,
  isFirstLoad,
  isRefreshing,
  load,
} = useDashboardStats();

onMounted(() => {
  void load();
});

const compact = computed(() => [
  {
    id: "sandbox",
    metric: sandbox.value,
    label: t("dashboard.cards.sandbox_24h"),
    color: "accent",
    icon: mdiInbox,
    href: "/sandbox",
  },
  {
    id: "webhooks",
    metric: webhooks.value,
    label: t("dashboard.cards.webhooks"),
    color: "secondary",
    icon: mdiWebhook,
    href: "/webhooks",
  },
  {
    id: "members",
    metric: members.value,
    label: t("dashboard.cards.members"),
    color: "primary",
    icon: mdiAccountGroup,
    href: "/members",
  },
]);

const barSegments = computed(() => {
  const total = stateTotal.value || 0;
  return stateSegments.value
    .filter((s) => (s.value ?? 0) > 0)
    .map((s) => ({
      ...s,
      pct: total > 0 ? ((s.value ?? 0) / total) * 100 : 0,
    }));
});

const charts = computed(() => {
  const all = [
    {
      id: "outbound",
      title: t("dashboard.chart_outbound_title"),
      description: t("dashboard.chart_outbound_desc"),
      slices: outboundSlices.value,
      kind: "bar" as const,
    },
    {
      id: "deliveries",
      title: t("dashboard.chart_deliveries_title"),
      description: t("dashboard.chart_deliveries_desc"),
      slices: deliverySlices.value,
      kind: "donut" as const,
    },
    {
      id: "logs",
      title: t("dashboard.chart_logs_title"),
      description: t("dashboard.chart_logs_desc"),
      slices: logLevelSlices.value,
      kind: "bar" as const,
    },
    {
      id: "queues",
      title: t("dashboard.chart_queues_title"),
      description: t("dashboard.chart_queues_desc"),
      slices: queueSlices.value,
      kind: "bar" as const,
    },
  ];
  return all.filter((c) => {
    if (c.id === "logs") return canReadLogLevels.value;
    if (c.id === "queues") return canReadQueues.value;
    return true;
  });
});

const fmt = (n: number | null | undefined): string =>
  n == null ? EMPTY_VALUE : String(n);

const open = (href: string): void => {
  void router.push(href);
};
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t("dashboard.title") }}
      </h1>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("dashboard.subtitle") }}
      </p>
    </div>

    <!-- W74 KPI cards: own period, polling and load state. -->
    <DashKpiSection />

    <!-- First-load skeleton: same paddings / bone sizes as loaded cards (no jump). -->
    <div
      v-if="isFirstLoad"
      :aria-label="t('common.states.loading')"
      aria-busy="true"
      role="status"
    >
      <v-row align="start">
        <v-col
          cols="12"
        >
          <AppCard>
            <div class="text-title-medium font-weight-bold mb-4">
              {{ t("dashboard.by_state") }}
            </div>
            <!-- Stack bar + legend chips, not a tall chart image. -->
            <v-skeleton-loader
              type="ossein, chip@6"
              class="mb-0"
            />
          </AppCard>
        </v-col>
        <v-col
          cols="12"
        >
          <v-row dense>
            <v-col
              v-for="n in 3"
              :key="`compact-skel-${n}`"
              cols="12"
              md="4"
            >
              <AppCard>
                <LoadingSkeleton shape="tile" />
              </AppCard>
            </v-col>
          </v-row>
        </v-col>
      </v-row>

      <v-row class="mt-2">
        <v-col
          v-for="chart in charts"
          :key="`chart-skel-${chart.id}`"
          cols="12"
          md="6"
        >
          <AppCard>
            <div class="text-title-medium font-weight-bold mb-1">
              &nbsp;
            </div>
            <p class="text-body-small text-medium-emphasis mb-3">
              &nbsp;
            </p>
            <v-skeleton-loader
              type="image"
              height="260"
            />
          </AppCard>
        </v-col>
      </v-row>
    </div>

    <div
      v-else
      class="position-relative"
    >
      <!-- align-start: by-state card sizes to content (no dead stretch beside the tiles). -->
      <v-row align="start">
        <v-col
          cols="12"
        >
          <AppCard>
            <div class="text-title-medium font-weight-bold mb-4">
              {{ t("dashboard.by_state") }}
            </div>

            <div
              v-if="stateTotal > 0"
              class="dash-stack mb-4"
              aria-hidden="true"
            >
              <span
                v-for="seg in barSegments"
                :key="seg.state"
                :style="{ width: `${seg.pct}%`, backgroundColor: `rgb(var(--v-theme-${seg.color}))` }"
                class="dash-stack__seg"
              />
            </div>
            <div
              v-else
              class="text-body-medium text-medium-emphasis mb-4"
            >
              {{ t("dashboard.by_state_empty") }}
            </div>

            <div class="d-flex flex-wrap ga-2">
              <button
                v-for="seg in stateSegments"
                :key="seg.state"
                class="dash-legend"
                type="button"
                @click="open(seg.href)"
              >
                <span
                  :style="{ backgroundColor: `rgb(var(--v-theme-${seg.color}))` }"
                  class="dash-legend__swatch"
                />
                <span
                  :class="{ 'text-medium-emphasis': !seg.value }"
                  class="text-body-small"
                >
                  {{ t(`dashboard.states.${seg.state}`) }}
                </span>
                <span
                  :class="{ 'text-medium-emphasis': !seg.value }"
                  class="text-body-small font-weight-bold text-numeric"
                >
                  {{ seg.loading ? "…" : fmt(seg.value) }}
                </span>
              </button>
            </div>
          </AppCard>
        </v-col>

        <v-col
          cols="12"
        >
          <v-row dense>
            <v-col
              v-for="card in compact"
              :key="card.id"
              cols="12"
              md="4"
            >
              <AppCard
                selectable
                @click="open(card.href)"
              >
                <div class="d-flex align-center ga-3">
                  <v-avatar
                    :color="card.color"
                    size="40"
                  >
                    <v-icon
                      :icon="card.icon"
                      size="20"
                    />
                  </v-avatar>
                  <div class="flex-grow-1 min-width-0">
                    <div class="text-label-large text-medium-emphasis">
                      {{ card.label }}
                    </div>
                    <div
                      v-if="card.metric.error"
                      class="text-body-small text-error"
                    >
                      {{ card.metric.error }}
                    </div>
                    <div
                      v-else
                      class="text-headline-small font-weight-bold text-numeric dash-compact__value"
                    >
                      {{ card.metric.loading ? "…" : fmt(card.metric.value) }}
                    </div>
                  </div>
                </div>
              </AppCard>
            </v-col>
          </v-row>
        </v-col>
      </v-row>

      <v-row class="mt-4">
        <v-col
          v-for="chart in charts"
          :key="chart.id"
          cols="12"
          md="6"
        >
          <AppCard>
            <Suspense>
              <DashStateChart
                :title="chart.title"
                :description="chart.description"
                :slices="chart.slices"
                :kind="chart.kind"
              />
              <template #fallback>
                <div>
                  <div class="text-title-medium font-weight-bold mb-1">
                    {{ chart.title }}
                  </div>
                  <p class="text-body-small text-medium-emphasis mb-3">
                    {{ chart.description }}
                  </p>
                  <v-skeleton-loader
                    type="image"
                    height="260"
                  />
                </div>
              </template>
            </Suspense>
          </AppCard>
        </v-col>
      </v-row>

      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.dash-compact__value {
  font-weight: 700 !important;
  font-variant-numeric: tabular-nums;
  letter-spacing: -0.02em;
  line-height: 1.1;
}

.dash-stack {
  display: flex;
  height: 14px;
  overflow: hidden;
  border-radius: 999px;
  background: rgb(var(--v-theme-on-surface), 0.06);
}

.dash-stack__seg {
  display: block;
  min-width: 2px;
  height: 100%;
}

.dash-legend {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  border: 0;
  padding: 4px 8px;
  border-radius: 8px;
  background: transparent;
  cursor: pointer;
  color: inherit;

  &:hover {
    background: rgb(var(--v-theme-on-surface), 0.04);
  }
}

.dash-legend__swatch {
  width: 10px;
  height: 10px;
  border-radius: 999px;
  flex-shrink: 0;
}
</style>
