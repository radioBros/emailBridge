<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiAlertCircle } from "@mdi/js";
// Static: chrome and placeholders paint on the first frame (loading-state rule).
import AppCard from "@/components/cards/AppCard.vue";
import KpiCard from "@/components/cards/KpiCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import KpiTrend from "@/features/home/components/KpiTrend.vue";
import DateRangeField from "@/components/DateRangeField.vue";
import { KPI_PERIODS, MAX_KPI_RANGE_DAYS } from "@/api/dashboard";
import { actionOf } from "@/constants/Actions";
import { useDashboardKpis } from "@/features/home/composables/useDashboardKpis";

const KpiSparkline = defineAsyncComponent(
  () => import("@/features/home/components/KpiSparkline.vue"),
);

const { t, locale } = useI18n();
const router = useRouter();
const {
  period,
  error,
  lastUpdated,
  cards,
  expectedCount,
  historyLimited,
  isFirstLoad,
  isRefreshing,
  init,
  retry,
  setPeriod,
  setCustomRange,
  customFrom,
  customTo,
  startPolling,
} = useDashboardKpis();

// The date field emits from and to one after the other, then change: apply the window on change.
const draftFrom = ref("");
const draftTo = ref("");
watch([customFrom, customTo], ([f, t]) => {
  draftFrom.value = f;
  draftTo.value = t;
}, { immediate: true });
const onRangeChange = (): void => setCustomRange(draftFrom.value, draftTo.value);

onMounted(async () => {
  await init();
  startPolling();
});

/** Skeleton until the first answer (or error) — also covers the frame before init starts. */
const showSkeleton = computed(() => isFirstLoad.value || (!cards.value.length && !error.value));

const updatedText = computed(() =>
  lastUpdated.value
    ? t("dashboard.kpis.updated", {
        time: new Intl.DateTimeFormat(locale.value, { timeStyle: "short" }).format(lastUpdated.value),
      })
    : "",
);

const open = (href: string): void => {
  void router.push(href);
};
</script>

<template>
  <section
    :aria-label="t('dashboard.kpis.title')"
    class="mb-6"
  >
    <div class="d-flex flex-column flex-sm-row align-sm-center justify-space-between ga-3 mb-4">
      <div class="min-width-0">
        <h2 class="text-title-large font-weight-bold mb-0">
          {{ t("dashboard.kpis.title") }}
        </h2>
        <p class="text-body-small text-medium-emphasis mb-0">
          <span>{{ t("dashboard.kpis.subtitle") }}</span>
          <span
            v-if="updatedText"
            class="text-no-wrap"
          >
            · {{ updatedText }}
          </span>
        </p>
      </div>
      <div class="d-flex flex-wrap align-center justify-sm-end ga-2 flex-shrink-0">
        <DateRangeField
          v-if="period === 'custom'"
          v-model:from="draftFrom"
          v-model:to="draftTo"
          :label="t('dashboard.kpis.custom_range', { days: MAX_KPI_RANGE_DAYS })"
          :max-days="MAX_KPI_RANGE_DAYS"
          data-testid="kpi-custom-range"
          no-future
          @change="onRangeChange"
        />
        <v-btn-toggle
          :model-value="period"
          :aria-label="t('dashboard.kpis.period_label')"
          class="flex-shrink-0"
          mandatory
          @update:model-value="setPeriod"
        >
          <v-btn
            v-for="p in KPI_PERIODS"
            :key="p"
            :value="p"
            :data-testid="`kpi-period-${p}`"
          >
            {{ t(`dashboard.kpis.period.${p}`) }}
          </v-btn>
          <v-btn
            value="custom"
            data-testid="kpi-period-custom"
          >
            {{ t("dashboard.kpis.period.custom") }}
          </v-btn>
        </v-btn-toggle>
      </div>
    </div>

    <!-- First load: the same v-cols with a real card each; only the contents stand in. -->
    <v-row
      v-if="showSkeleton"
      :aria-label="t('common.states.loading')"
      aria-busy="true"
      role="status"
    >
      <v-col
        v-for="n in expectedCount"
        :key="`kpi-skel-${n}`"
        cols="12"
        sm="6"
        lg="3"
      >
        <AppCard class="h-100">
          <v-skeleton-loader type="text, heading" />
        </AppCard>
      </v-col>
    </v-row>

    <AppCard
      v-else-if="error && !cards.length"
      class="text-center"
    >
      <div class="d-flex flex-column align-center ga-3 py-4">
        <v-icon
          :icon="mdiAlertCircle"
          color="error"
        />
        <div class="text-body-medium">
          {{ t("dashboard.kpis.error") }}
        </div>
        <div class="text-body-small text-medium-emphasis">
          {{ error }}
        </div>
        <v-btn
          :color="actionOf('retry').color"
          @click="retry"
        >
          {{ t("dashboard.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <div
      v-else
      class="position-relative"
    >
      <div
        v-if="error"
        class="d-flex align-center flex-wrap ga-2 mb-3 text-body-small text-error"
        role="alert"
        data-testid="kpi-refetch-error"
      >
        <v-icon
          :icon="mdiAlertCircle"
          aria-hidden="true"
        />
        <span>{{ t("dashboard.kpis.refetch_error") }}</span>
        <v-btn
          :color="actionOf('retry').color"
          @click="retry"
        >
          {{ t("dashboard.retry") }}
        </v-btn>
      </div>
      <v-row>
        <v-col
          v-for="card in cards"
          :key="card.id"
          :data-testid="`kpi-${card.id}`"
          cols="12"
          sm="6"
          lg="3"
        >
          <KpiCard
            v-tippy="card.tooltip"
            :label="card.label"
            :accent="card.accent"
            :value="card.value"
            selectable
            @click="open(card.href)"
          >
            <template #icon>
              <v-avatar :color="card.accent">
                <v-icon :icon="card.icon" />
              </v-avatar>
            </template>
            <template #meta>
              <KpiTrend
                v-if="card.kpi"
                :current="card.kpi.current"
                :previous="card.kpi.previous"
                :polarity="card.polarity"
                :partial="card.kpi.partial"
              />
              <span
                v-else
                class="text-truncate d-block"
              >
                {{ card.sub }}
              </span>
            </template>
            <template
              v-if="card.kpi"
              #chart
            >
              <KpiSparkline
                :values="card.kpi.buckets ?? []"
                :color="card.accent"
              />
            </template>
          </KpiCard>
        </v-col>
      </v-row>
      <p
        v-if="historyLimited"
        class="text-body-small text-medium-emphasis mt-2 mb-0"
      >
        {{ t("dashboard.kpis.retention_note") }}
      </p>
      <AppSectionLoader :status="isRefreshing" />
    </div>
  </section>
</template>
