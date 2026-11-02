<template>
  <AppCard
    :selectable="selectable"
    :aria-label="label"
    :role="selectable ? 'link' : undefined"
    :tabindex="selectable ? 0 : undefined"
    class="kpi-card h-100"
    @click="emit('click', $event)"
    @keydown.enter="onKey"
  >
    <div class="d-flex flex-column ga-2 h-100">
      <div class="d-flex align-start justify-space-between ga-3">
        <div class="kpi-card__label text-label-large text-medium-emphasis">
          {{ label }}
        </div>
        <slot
          :accent="accent"
          name="icon"
        />
      </div>

      <div class="d-flex align-end justify-space-between ga-3">
        <div class="min-width-0">
          <div class="kpi-card__value text-headline-medium font-weight-bold text-numeric">
            <slot name="value">
              {{ value }}
            </slot>
          </div>
          <div
            v-if="$slots.meta"
            class="text-body-small text-medium-emphasis mt-1"
          >
            <slot name="meta" />
          </div>
        </div>
        <div
          v-if="$slots.chart"
          class="kpi-card__chart flex-shrink-0"
        >
          <slot
            :accent="accent"
            name="chart"
          />
        </div>
      </div>

      <div
        v-if="$slots.footer"
        class="text-body-small text-medium-emphasis mt-auto"
      >
        <slot name="footer" />
      </div>
    </div>
  </AppCard>
</template>

<script setup lang="ts">
  // Static import: the card is chrome that must paint on the first frame (loading-state rule).
  import AppCard from "@/components/cards/AppCard.vue";

  /**
   * A count card on the kit `AppCard`: label, icon, big value, an optional meta line (trend), a chart
   * slot (sparkline) and a footer. Only scalars are props; everything that varies per consumer is a
   * slot, so a new KPI never edits this file.
   *
   * `accent` is a theme token (`success`, `warning`, `error`, `info` …) passed to the `icon` and
   * `chart` slots, so the consumer colours its own icon and sparkline consistently.
   */
  const props = withDefaults(
    defineProps<{
      label: string;
      /** Theme colour token describing the card's health. */
      accent?: string;
      /** Formatted value; the `value` slot overrides it. */
      value?: string;
      /** Clickable card (opens the filtered list). */
      selectable?: boolean;
    }>(),
    { accent: "primary", value: "", selectable: false },
  );

  const emit = defineEmits<{
    click: [event: Event];
  }>();

  /** Enter opens a selectable card like a click (it is focusable with role="link"). */
  const onKey = (event: KeyboardEvent): void => {
    if (props.selectable) emit("click", event);
  };
</script>

<style lang="scss" scoped>
  .kpi-card__label {
    min-width: 0;
    flex: 1 1 auto;
    display: -webkit-box;
    overflow: hidden;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    white-space: normal;
    overflow-wrap: anywhere;
  }

  .kpi-card__value {
    font-variant-numeric: tabular-nums;
    letter-spacing: -0.02em;
    line-height: 1.1;
  }

  .kpi-card__chart {
    width: 96px;
    height: 36px;
  }
</style>
