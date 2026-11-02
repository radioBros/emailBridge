<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from "vue";
import { useTheme } from "vuetify";
import { use } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { LineChart } from "echarts/charts";
import { GridComponent } from "echarts/components";

use([CanvasRenderer, LineChart, GridComponent]);

/**
 * A tiny trend line for a KPI card (W74). Loaded lazily by the dashboard; vue-echarts itself is a
 * second lazy chunk so the card grid paints before any chart code arrives.
 */
const props = defineProps<{
  values: number[];
  /** Theme colour token (success, warning, error, info, primary …). */
  color: string;
}>();

const theme = useTheme();
const VChart = shallowRef<unknown>(null);
const ready = ref(false);

onMounted(async () => {
  const mod = await import("vue-echarts");
  VChart.value = mod.default;
  ready.value = true;
});

const stroke = computed(() => {
  void theme.current.value.dark;
  const raw = theme.current.value.colors[props.color];
  return typeof raw === "string" && raw ? raw : "#888888";
});

const option = computed(() => ({
  animation: false,
  backgroundColor: "transparent",
  grid: { left: 0, right: 0, top: 2, bottom: 2 },
  xAxis: { type: "category", show: false, boundaryGap: false, data: props.values.map((_, i) => i) },
  yAxis: { type: "value", show: false, min: 0 },
  series: [
    {
      type: "line",
      data: props.values,
      smooth: 0.3,
      symbol: "none",
      lineStyle: { width: 1.5, color: stroke.value },
      areaStyle: { color: stroke.value, opacity: 0.12 },
    },
  ],
}));

const hasData = computed(() => props.values.some((v) => v > 0));
</script>

<template>
  <component
    :is="VChart"
    v-if="ready && hasData"
    :option="option"
    class="w-100 h-100"
    aria-hidden="true"
    autoresize
  />
  <div
    v-else
    class="kpi-spark__base w-100 h-100"
    aria-hidden="true"
  />
</template>

<style lang="scss" scoped>
.kpi-spark__base {
  background: linear-gradient(
    to top,
    rgb(var(--v-theme-on-surface), 0.12) 0,
    rgb(var(--v-theme-on-surface), 0.12) 1px,
    transparent 1px
  );
}
</style>
