<script setup lang="ts">
import { computed, onMounted, toRef, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiStethoscope } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import { useNotifyStore } from "@/stores/notify";
import { useAccountHealth } from "@/features/health/composables/useAccountHealth";
import {
  hintDetailKey,
  hintTitleKey,
  severityColor,
  severityIcon,
  severityLabelKey,
} from "@/features/health/helpers/severity";

const props = defineProps<{
  accountId: string;
}>();

const { t, te, locale } = useI18n();
const notify = useNotifyStore();
const checkAction = actionOf("verify");

const accountId = toRef(props, "accountId");
const {
  health,
  loadError,
  checkError,
  checking,
  isFirstLoad,
  isRefreshing,
  load,
  runCheck,
} = useAccountHealth(accountId);

onMounted(() => {
  void load();
});

watch(accountId, () => {
  void load();
});

const formatWhen = (iso?: string): string => {
  if (!iso) return EMPTY_VALUE;
  return DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);
};

const sevLabel = (sev: string): string => {
  const k = severityLabelKey(sev);
  return te(k) ? t(k) : sev;
};

const hintTitle = (code: string, fallback: string): string => {
  const k = hintTitleKey(code);
  return te(k) ? t(k) : fallback;
};

const hintDetail = (code: string, fallback: string): string => {
  const k = hintDetailKey(code);
  return te(k) ? t(k) : fallback;
};

const yesNo = (v?: boolean): string => (v ? t("health.fact_yes") : t("health.fact_no"));

const onCheck = async (): Promise<void> => {
  const ok = await runCheck();
  if (ok && health.value?.liveCheck?.ok) {
    notify.push({ message: t("health.check_ok"), color: "success" });
  } else if (ok) {
    notify.push({ message: t("health.check_fail"), color: "warning" });
  }
};

const facts = computed(() => health.value?.facts);
</script>

<template>
  <div class="position-relative">
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="4"
      shape="card"
    />
    <template v-else>
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

      <AppCard v-else-if="health">
        <template #header>
          <div class="d-flex flex-wrap align-center justify-space-between ga-2 w-100">
            <div class="d-flex align-center ga-2">
              <v-avatar-small
                :color="severityColor(health.severity)"
                :icon="severityIcon(health.severity)"
              />
              <div>
                <div class="text-title-medium font-weight-bold">
                  {{ t("health.panel_title") }}
                </div>
                <div class="text-body-small text-medium-emphasis">
                  {{ health.summary || t("health.summary_empty") }}
                </div>
              </div>
            </div>
            <div class="d-flex align-center ga-2">
              <v-chip
                :color="severityColor(health.severity)"
                :prepend-icon="severityIcon(health.severity)"
                label
              >
                {{ sevLabel(health.severity) }}
              </v-chip>
              <v-btn
                :color="checkAction.color"
                :prepend-icon="mdiStethoscope"
                :loading="checking"
                @click="onCheck"
              >
                {{ t("health.run_check") }}
              </v-btn>
            </div>
          </div>
        </template>

        <v-alert
          v-if="checkError"
          type="error"
          class="mb-4"
        >
          {{ checkError }}
        </v-alert>

        <v-alert
          v-if="health.liveCheck"
          :type="health.liveCheck.ok ? 'success' : 'warning'"
          class="mb-4"
        >
          <template v-if="health.liveCheck.ok">
            {{ t("health.live_ok", { ms: health.liveCheck.latencyMs ?? 0 }) }}
          </template>
          <template v-else>
            {{ t("health.live_fail") }}
            <span v-if="health.liveCheck.error?.message">
              — {{ health.liveCheck.error.message }}
            </span>
          </template>
        </v-alert>

        <div
          v-if="health.hints?.length"
          class="mb-4"
        >
          <div class="text-label-large font-weight-bold mb-2">
            {{ t("health.hints_title") }}
          </div>
          <v-alert
            v-for="hint in health.hints"
            :key="hint.code"
            :color="severityColor(hint.severity)"
            class="mb-2"
          >
            <div class="font-weight-medium">
              {{ hintTitle(hint.code, hint.title) }}
            </div>
            <div class="text-body-small">
              {{ hintDetail(hint.code, hint.detail) }}
            </div>
          </v-alert>
        </div>

        <div
          v-if="facts"
          class="mb-4"
        >
          <div class="text-label-large font-weight-bold mb-2">
            {{ t("health.facts_title") }}
          </div>
          <v-row>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_tls") }}
              </div>
              <div class="text-body-medium">
                {{ facts.tlsMode || EMPTY_VALUE }}
              </div>
            </v-col>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_provider") }}
              </div>
              <div class="text-body-medium">
                {{ facts.provider || EMPTY_VALUE }}
              </div>
            </v-col>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_folders") }}
              </div>
              <div class="text-body-medium">
                {{ facts.folderCount ?? 0 }}
              </div>
            </v-col>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_idle") }}
              </div>
              <div class="text-body-medium">
                {{ yesNo(facts.idle) }}
              </div>
            </v-col>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_condstore") }}
              </div>
              <div class="text-body-medium">
                {{ yesNo(facts.condstore) }}
              </div>
            </v-col>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_qresync") }}
              </div>
              <div class="text-body-medium">
                {{ yesNo(facts.qresync) }}
              </div>
            </v-col>
            <v-col
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_last_sync") }}
              </div>
              <div class="text-body-medium">
                {{ formatWhen(facts.lastSyncAt) }}
              </div>
            </v-col>
            <v-col
              v-if="facts.connectLatencyMs != null"
              cols="6"
              md="4"
            >
              <div class="text-caption text-medium-emphasis">
                {{ t("health.fact_latency") }}
              </div>
              <div class="text-body-medium">
                {{ facts.connectLatencyMs }} ms
              </div>
            </v-col>
          </v-row>
        </div>

        <div>
          <div class="text-label-large font-weight-bold mb-2">
            {{ t("health.errors_title") }}
          </div>
          <div
            v-if="!health.recentErrors?.length"
            class="text-body-medium text-medium-emphasis"
          >
            {{ t("health.errors_empty") }}
          </div>
          <v-timeline
            v-else
            side="end"
          >
            <v-timeline-item
              v-for="(err, idx) in health.recentErrors"
              :key="idx"
              :dot-color="err.kind === 'auth' ? 'error' : 'warning'"
            >
              <div class="text-caption text-medium-emphasis">
                {{ formatWhen(err.at) }}
              </div>
              <div class="text-body-medium font-weight-medium">
                {{ err.code }}
              </div>
              <div
                v-if="err.response"
                class="text-body-small text-medium-emphasis"
              >
                {{ err.response }}
              </div>
            </v-timeline-item>
          </v-timeline>
        </div>
      </AppCard>
    </template>
    <AppSectionLoader :status="isRefreshing" />
  </div>
</template>
