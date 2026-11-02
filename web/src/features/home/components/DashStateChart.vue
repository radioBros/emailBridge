<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useTheme } from "vuetify";
import { use } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { BarChart, PieChart } from "echarts/charts";
import {
  GridComponent,
  LegendComponent,
  TooltipComponent,
} from "echarts/components";
import type { ChartSlice } from "@/features/home/composables/useDashboardStats";

use([
  CanvasRenderer,
  BarChart,
  PieChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
]);

const props = withDefaults(
  defineProps<{
    title: string;
    description: string;
    slices: ChartSlice[];
    kind?: "bar" | "donut";
  }>(),
  { kind: "bar" },
);

const { t, te } = useI18n();
const theme = useTheme();
const VChart = ref<unknown>(null);
const ready = ref(false);

onMounted(async () => {
  const mod = await import("vue-echarts");
  VChart.value = mod.default;
  ready.value = true;
});

const labelOf = (slice: ChartSlice): string =>
  te(slice.labelKey) ? t(slice.labelKey) : slice.key;

/** Theme token → colour from Vuetify's reactive theme (survives light/dark toggles). */
const colorOf = (token: string): string => {
  const raw = theme.current.value.colors[token];
  return typeof raw === "string" && raw ? raw : "#888888";
};

const textColor = computed(() => colorOf("on-surface") || "#1E2433");
const mutedColor = computed(() => colorOf("on-surface-variant") || textColor.value);

const option = computed(() => {
  // Depend on the live theme so OS "system" flips and shell toggles re-paint.
  void theme.name.value;
  void theme.current.value.dark;
  void theme.current.value.colors;
  const labels = props.slices.map((s) => labelOf(s));
  const values = props.slices.map((s) => s.value);
  const colors = props.slices.map((s) => colorOf(s.color));
  const axis = mutedColor.value;
  const label = textColor.value;

  if (props.kind === "donut") {
    return {
      animation: false,
      backgroundColor: "transparent",
      color: colors,
      tooltip: {
        trigger: "item",
        formatter: "{b}: {c} ({d}%)",
      },
      legend: {
        bottom: 0,
        textStyle: { color: axis },
      },
      series: [
        {
          type: "pie",
          radius: ["42%", "68%"],
          center: ["50%", "45%"],
          avoidLabelOverlap: true,
          label: { show: false },
          data: labels.map((name, i) => ({
            name,
            value: values[i],
          })),
        },
      ],
    };
  }

  return {
    animation: false,
    backgroundColor: "transparent",
    color: colors,
    grid: { left: 8, right: 24, top: 8, bottom: 8, containLabel: true },
    tooltip: { trigger: "axis", axisPointer: { type: "shadow" } },
    xAxis: {
      type: "value",
      axisLabel: { color: axis },
      splitLine: {
        lineStyle: {
          color: colorOf("on-surface"),
          opacity: 0.12,
        },
      },
    },
    yAxis: {
      type: "category",
      data: labels,
      axisLabel: { color: label },
      axisTick: { show: false },
      axisLine: { show: false },
    },
    series: [
      {
        type: "bar",
        data: values.map((v, i) => ({
          value: v,
          itemStyle: { color: colors[i], borderRadius: [0, 6, 6, 0] },
        })),
        barMaxWidth: 22,
      },
    ],
  };
});
</script>

<template>
  <div
    :aria-label="title"
    :aria-description="description"
    class="dash-chart"
    role="img"
  >
    <div class="text-title-medium font-weight-bold mb-1">
      {{ title }}
    </div>
    <p class="text-body-small text-medium-emphasis mb-3">
      {{ description }}
    </p>

    <div
      v-if="!slices.length"
      class="text-body-medium text-medium-emphasis py-8 text-center"
    >
      {{ t("dashboard.chart_empty") }}
    </div>
    <component
      :is="VChart"
      v-else-if="ready"
      :option="option"
      class="dash-chart__canvas"
      autoresize
    />
    <v-skeleton-loader
      v-else
      type="image"
      class="dash-chart__skel"
    />
  </div>
</template>

<style lang="scss" scoped>
.dash-chart__canvas {
  width: 100%;
  height: 260px;
}

.dash-chart__skel {
  height: 260px;
}
</style>
