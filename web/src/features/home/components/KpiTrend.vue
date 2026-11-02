<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiMinus, mdiTrendingDown, mdiTrendingUp } from "@mdi/js";
import { computeTrend, type TrendPolarity } from "@/features/home/helpers/kpiHealth";

/** Trend of a KPI against the previous equal period (W74 §3). */
const props = defineProps<{
  current: number;
  previous: number | null | undefined;
  polarity: TrendPolarity;
  /** The current period is only partly retained. */
  partial?: boolean;
}>();

const { t } = useI18n();

const trend = computed(() => computeTrend(props.current, props.previous, props.polarity));

const icon = computed(() => {
  if (trend.value.kind === "up") return mdiTrendingUp;
  if (trend.value.kind === "down") return mdiTrendingDown;
  return mdiMinus;
});

const text = computed(() => {
  switch (trend.value.kind) {
    case "none":
      return t("dashboard.kpis.trend.none");
    case "new":
      return t("dashboard.kpis.trend.new");
    case "flat":
      return t("dashboard.kpis.trend.flat");
    default:
      return t("dashboard.kpis.trend.pct", { pct: trend.value.pct });
  }
});

const hint = computed(() => {
  if (trend.value.kind === "none") return t("dashboard.kpis.trend.none_hint");
  if (props.partial) return t("dashboard.kpis.trend.partial_hint");
  return t("dashboard.kpis.trend.vs_previous");
});
</script>

<template>
  <span
    v-tippy="hint"
    :class="`text-${trend.color}`"
    class="d-inline-flex align-center ga-1 text-no-wrap"
  >
    <v-icon
      :icon="icon"
      aria-hidden="true"
    />
    <span class="font-weight-medium">{{ text }}</span>
    <span class="text-medium-emphasis">{{ t("dashboard.kpis.trend.vs") }}</span>
  </span>
</template>
