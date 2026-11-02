<script setup lang="ts">
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiHeartPulse } from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import { AccountStates } from "@/constants/Statuses";
import { useOrgHealth } from "@/features/health/composables/useOrgHealth";
import {
  SEVERITIES,
  hintTitleKey,
  severityColor,
  severityIcon,
  severityLabelKey,
} from "@/features/health/helpers/severity";

const { t, te, locale } = useI18n();
const router = useRouter();

const {
  rows,
  counts,
  total,
  page,
  ipp,
  search,
  severity,
  sortBy,
  loadError,
  isFirstLoad,
  isRefreshing,
  load,
  setPage,
  setIpp,
  setSortBy,
  setSearch,
  setSeverity,
} = useOrgHealth();

onMounted(() => {
  void load();
});

const headers = computed<ServerTableHeader[]>(() => [
  { key: "severity", title: t("health.col_severity"), sortable: true },
  { key: "name", title: t("health.col_name"), forced: true },
  { key: "email", title: t("health.col_email"), sortable: false },
  { key: "state", title: t("health.col_state"), sortable: true },
  { key: "summary", title: t("health.col_summary"), sortable: false },
  { key: "lastSyncAt", title: t("health.col_last_sync"), sortable: true },
]);

const severityFilterItems = computed(() => [
  { title: t("health.severity_all"), value: "" },
  ...SEVERITIES.map((s) => ({ title: t(severityLabelKey(s)), value: s })),
]);

const sevLabel = (sev: string): string => {
  const k = severityLabelKey(sev);
  return te(k) ? t(k) : sev;
};

const stateLabel = (state: string): string => {
  const k = `health.state_${state}`;
  return te(k) ? t(k) : state;
};

const stateColor = (state: string): string => {
  const spec = (AccountStates as Record<string, { color: string }>)[state];
  return spec?.color ?? "grey";
};

const formatWhen = (iso?: string): string => {
  if (!iso) return EMPTY_VALUE;
  return DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);
};

const rowSummary = (item: { summary: string; hintCodes?: string[] }): string => {
  const code = item.hintCodes?.[0];
  if (code) {
    const k = hintTitleKey(code);
    if (te(k)) return t(k);
  }
  return item.summary;
};

const openAccount = (id: string): void => {
  void router.push(`/accounts/${id}`);
};

const countChips = computed(() => [
  { key: "critical", n: counts.value.critical, color: "error" as const },
  { key: "warning", n: counts.value.warning, color: "warning" as const },
  { key: "info", n: counts.value.info, color: "info" as const },
  { key: "ok", n: counts.value.ok, color: "success" as const },
]);
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-2 mb-4">
      <div class="d-flex align-center ga-3">
        <v-avatar
          :icon="mdiHeartPulse"
          color="error"
        />
        <div>
          <h1 class="text-h5 font-weight-bold mb-0">
            {{ t("health.title") }}
          </h1>
          <div class="text-body-small text-medium-emphasis">
            {{ t("health.subtitle") }}
          </div>
        </div>
      </div>
      <div class="d-flex flex-wrap ga-2">
        <v-chip
          v-for="c in countChips"
          :key="c.key"
          :color="c.color"
          label
        >
          {{ t(`health.counts_${c.key}`) }}: {{ c.n }}
        </v-chip>
      </div>
    </div>

    <v-alert
      v-if="loadError"
      type="error"
      class="mb-4"
    >
      {{ loadError }}
      <template #append>
        <v-btn
          @click="load"
        >
          {{ t("health.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <template v-else>
      <div class="d-flex flex-wrap align-center ga-3 mb-4">
        <v-select
          :model-value="severity"
          :items="severityFilterItems"
          :label="t('health.filter_severity')"
          item-title="title"
          item-value="value"
          class="no-validation"
          style="max-width: 220px"
          hide-details
          @update:model-value="(v) => setSeverity(v)"
        />
      </div>

      <TableCard
        :page="page"
        :ipp="ipp"
        :sort-by="sortBy"
        :headers="headers"
        :items="rows"
        :total="total"
        :loading="isFirstLoad || isRefreshing"
        :search="search"
        table-name="org-account-health"
        item-value="accountId"
        @update:page="setPage"
        @update:ipp="setIpp"
        @update:sort-by="setSortBy"
        @update:search="setSearch"
        @click:row="(_e, row) => openAccount(row.item.accountId)"
      >
        <template #[`item.severity`]="{ item }">
          <v-chip
            :color="severityColor(item.severity)"
            :prepend-icon="severityIcon(item.severity)"
            label
          >
            {{ sevLabel(item.severity) }}
          </v-chip>
        </template>
        <template #[`item.state`]="{ item }">
          <v-chip
            :color="stateColor(item.state)"
            label
          >
            {{ stateLabel(item.state) }}
          </v-chip>
        </template>
        <template #[`item.summary`]="{ item }">
          {{ rowSummary(item) }}
        </template>
        <template #[`item.lastSyncAt`]="{ item }">
          {{ formatWhen(item.lastSyncAt) }}
        </template>
        <template #no-data>
          <div class="text-center py-8">
            <div class="text-body-medium mb-1">
              {{ t("health.empty") }}
            </div>
            <div class="text-body-small text-medium-emphasis">
              {{ t("health.empty_hint") }}
            </div>
          </div>
        </template>
      </TableCard>
    </template>
  </div>
</template>
