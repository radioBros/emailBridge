<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import { mdiAlertCircle, mdiPauseCircle, mdiRefresh, mdiTextBoxOutline } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import KpiCard from "@/components/cards/KpiCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import { actionOf } from "@/constants/Actions";
import { EMPTY_VALUE } from "@/helpers/Format";
import { useRealtime } from "@/composables/useRealtime";
import { useSessionStore } from "@/stores/session";
import type { QueueScope, QueueSummary } from "@/api/queues";
import QueueItemsPanel from "@/features/queues/components/QueueItemsPanel.vue";
import { useQueueSummaries } from "@/features/queues/composables/useQueueSummaries";
import {
  ageOf,
  backlogOf,
  canCancelIn,
  EVENTS_QUEUE,
  summaryOf,
  visibleQueues,
  type QueueDef,
} from "@/features/queues/helpers/queues";

const { t, n } = useI18n();
const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const refreshAction = actionOf("refresh");
const pauseAction = actionOf("pause");

const isOwner = computed(() => session.profile?.instanceRole === "owner");
const permissions = computed(() => session.me?.permissions ?? []);
const queues = computed(() => visibleQueues(isOwner.value));

const scope = ref<QueueScope>(route.query.scope === "all" ? "all" : "organization");
const initialQueue = typeof route.query.queue === "string" ? route.query.queue : "";
const tab = ref<string>(initialQueue || "EMAILBRIDGE_SUBMIT");

const { summaries, error, isFirstLoad, isRefreshing, load } = useQueueSummaries(scope);
const panel = ref<{ load: () => Promise<void> } | null>(null);

const activeDef = computed<QueueDef | undefined>(() => queues.value.find((q) => q.name === tab.value));

const canCancel = computed(() =>
  activeDef.value ? canCancelIn(activeDef.value, permissions.value, isOwner.value) : false,
);

// ---- header cards -----------------------------------------------------------------------------

interface CardView {
  name: string;
  label: string;
  icon: string;
  accent: string;
  value: string;
  sub: string;
  age: string;
  paused: boolean;
  selectable: boolean;
}

const ageText = (iso: string | undefined): string => {
  const a = ageOf(iso);
  return a ? t(`queues.age_${a.unit}`, { n: a.value }) : t("queues.age_none");
};

const accentOf = (s: QueueSummary | undefined): string => {
  const c = s?.items;
  if (s?.paused) return "warning";
  if (!c) return "secondary";
  if (c.deadLettered > 0) return "error";
  if (c.retrying > 0) return "warning";
  return "success";
};

const cards = computed<CardView[]>(() => {
  const out: CardView[] = queues.value.map((def) => {
    const s = summaryOf(summaries.value, def.name);
    const c = s?.items;
    const parts = [t("queues.card_retrying", { n: c?.retrying ?? 0 })];
    if (def.key === "sending") parts.unshift(t("queues.card_scheduled", { n: c?.scheduled ?? 0 }));
    if (def.key === "webhooks") parts.push(t("queues.card_dead", { n: c?.deadLettered ?? 0 }));
    return {
      name: def.name,
      label: t(`queues.queue_${def.key}`),
      icon: def.icon,
      accent: accentOf(s),
      value: n(backlogOf(c)),
      sub: parts.join(" · "),
      age: t("queues.card_oldest", { age: ageText(c?.oldestAt) }),
      paused: Boolean(s?.paused),
      selectable: true,
    };
  });
  const ev = summaryOf(summaries.value, EVENTS_QUEUE);
  out.push({
    name: EVENTS_QUEUE,
    label: t("queues.queue_events"),
    icon: mdiTextBoxOutline,
    accent: ev?.paused ? "warning" : "info",
    value: ev?.pending !== undefined ? n(ev.pending) : EMPTY_VALUE,
    sub: ev?.ackPending !== undefined ? t("queues.card_in_progress", { n: ev.ackPending }) : t("queues.card_read_only"),
    age: t("queues.card_events_note"),
    paused: Boolean(ev?.paused),
    selectable: false,
  });
  return out;
});

const openQueue = (name: string): void => {
  if (name !== EVENTS_QUEUE) tab.value = name;
};

// ---- refresh: realtime + manual ---------------------------------------------------------------

let timer: ReturnType<typeof setTimeout> | null = null;
const refreshAll = (): void => {
  void load();
  void panel.value?.load();
};
const scheduleRefresh = (): void => {
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => {
    timer = null;
    refreshAll();
  }, 600);
};

useRealtime({
  subscribe: { id: "queues-page", topic: "queues.updates" },
  onMessage: (msg) => {
    if (msg.topic === "queues.updates") scheduleRefresh();
  },
  onReconnect: refreshAll,
});
useRealtime({
  subscribe: { id: "queues-page-outbox", topic: "outbox.updates" },
  onMessage: (msg) => {
    if (msg.topic === "outbox.updates") scheduleRefresh();
  },
});

onUnmounted(() => {
  if (timer) clearTimeout(timer);
});

watch([tab, scope], ([q, s]) => {
  void router.replace({ query: { queue: q, ...(s === "all" ? { scope: "all" } : {}) } });
});
watch(scope, () => {
  void load();
});
watch(queues, (list) => {
  if (!list.some((q) => q.name === tab.value)) tab.value = list[0]?.name ?? "EMAILBRIDGE_SUBMIT";
});

onMounted(() => {
  void load();
});
</script>

<template>
  <div>
    <div class="mb-6 d-flex flex-wrap align-center justify-space-between ga-3">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("queues.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("queues.subtitle") }}
        </p>
      </div>
      <div class="d-flex flex-wrap align-center ga-2">
        <v-btn-toggle
          v-if="isOwner"
          v-model="scope"
          :aria-label="t('queues.scope_label')"
          color="primary"
          data-testid="queue-scope"
          mandatory
        >
          <v-btn value="organization">
            {{ t("queues.scope_org") }}
          </v-btn>
          <v-btn value="all">
            {{ t("queues.scope_all") }}
          </v-btn>
        </v-btn-toggle>
        <v-btn
          :color="refreshAction.color"
          :prepend-icon="mdiRefresh"
          data-testid="queue-refresh"
          @click="refreshAll"
        >
          {{ t("queues.refresh") }}
        </v-btn>
      </div>
    </div>

    <AppCard
      v-if="error"
      class="mb-4"
    >
      <div class="d-flex align-center ga-4 pa-2">
        <v-avatar-lead color="error">
          <v-icon :icon="mdiAlertCircle" />
        </v-avatar-lead>
        <div class="flex-grow-1 text-body-large">
          {{ error }}
        </div>
        <v-btn
          :color="refreshAction.color"
          :prepend-icon="mdiRefresh"
          @click="load"
        >
          {{ t("queues.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <div
      v-if="!error || summaries.length"
      class="position-relative mb-6"
    >
      <v-row>
        <v-col
          v-for="card in cards"
          :key="card.name"
          :data-testid="`queue-card-${card.name}`"
          cols="6"
          lg="3"
        >
          <KpiCard
            :label="card.label"
            :accent="card.accent"
            :value="card.value"
            :selectable="card.selectable"
            @click="openQueue(card.name)"
          >
            <template #icon>
              <v-avatar :color="card.accent">
                <v-icon :icon="card.icon" />
              </v-avatar>
            </template>
            <template
              v-if="isFirstLoad"
              #value
            >
              <v-skeleton-loader
                class="bg-transparent"
                type="heading"
                width="96"
              />
            </template>
            <template #meta>
              <v-skeleton-loader
                v-if="isFirstLoad"
                class="bg-transparent"
                type="text"
                width="160"
              />
              <span
                v-else
                class="d-block text-wrap"
              >
                {{ card.sub }}
              </span>
            </template>
            <template #footer>
              <div class="d-flex align-center flex-wrap ga-2">
                <v-skeleton-loader
                  v-if="isFirstLoad"
                  class="bg-transparent"
                  type="text"
                  width="120"
                />
                <span
                  v-else
                  class="text-truncate"
                >{{ card.age }}</span>
                <v-chip
                  v-if="card.paused"
                  :color="pauseAction.color"
                  :prepend-icon="mdiPauseCircle"
                  label
                >
                  {{ t("queues.paused") }}
                </v-chip>
              </div>
            </template>
          </KpiCard>
        </v-col>
      </v-row>
      <AppSectionLoader :status="isRefreshing" />
    </div>

    <v-tabs
      v-model="tab"
      class="mb-4"
      show-arrows
    >
      <v-tab
        v-for="def in queues"
        :key="def.name"
        :value="def.name"
        :prepend-icon="def.icon"
        :data-testid="`queue-tab-${def.name}`"
      >
        {{ t(`queues.queue_${def.key}`) }}
      </v-tab>
    </v-tabs>

    <QueueItemsPanel
      v-if="activeDef"
      ref="panel"
      :key="`${tab}-${scope}`"
      :queue="tab"
      :scope="scope"
      :can-cancel="canCancel"
      :is-owner="isOwner"
      @changed="load"
    />
  </div>
</template>
