<script setup lang="ts">
import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiLan,
  mdiPencil,
  mdiRefresh,
  mdiShieldLock,
  mdiWaterOutline,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import { EMPTY_VALUE } from "@/helpers/Format";
import type { ClusterNode, QueueView } from "@/api/cluster";
import { useCluster } from "@/features/cluster/composables/useCluster";
import {
  formatPartitions,
  formatSeenAt,
  formatUptime,
} from "@/features/cluster/helpers/format";
import { queueLabelKey, roleColor } from "@/features/cluster/helpers/roles";
import QueueKeepDialog from "@/features/cluster/components/QueueKeepDialog.vue";

const { t, locale, te } = useI18n();
const confirm = useConfirm();
const {
  nodes,
  queues,
  nodesTotal,
  queuesTotal,
  leader,
  totals,
  nodeCount,
  loadError,
  forbidden,
  isOwner,
  isFirstLoad,
  tableLoading,
  queuesTableLoading,
  mutating,
  page,
  ipp,
  sortBy,
  queuePage,
  queueIpp,
  queueSortBy,
  load,
  drain,
  setPaused,
  setKeep,
} = useCluster();

const keepOpen = ref(false);
const keepQueue = ref<QueueView | null>(null);

const nodeHeaders = computed<ServerTableHeader[]>(() => [
  { key: "id", title: t("cluster.col_node"), sortable: true, forced: true },
  { key: "roles", title: t("cluster.col_roles"), sortable: false },
  { key: "version", title: t("cluster.col_version"), sortable: true },
  { key: "uptime", title: t("cluster.col_uptime"), sortable: false },
  { key: "seenAt", title: t("cluster.col_seen"), sortable: true },
  { key: "accounts", title: t("cluster.col_accounts"), sortable: true },
  { key: "partitions", title: t("cluster.col_partitions"), sortable: false },
  { key: "status", title: t("cluster.col_status"), sortable: false },
  { key: "actions", title: t("cluster.col_actions"), sortable: false, forced: true },
]);

const queueHeaders = computed<ServerTableHeader[]>(() => [
  { key: "name", title: t("cluster.col_queue"), sortable: true, forced: true },
  { key: "pending", title: t("cluster.col_pending"), sortable: true },
  { key: "ackPending", title: t("cluster.col_ack"), sortable: true },
  { key: "consumers", title: t("cluster.col_consumers"), sortable: true },
  { key: "paused", title: t("cluster.col_paused"), sortable: false },
  { key: "keep", title: t("cluster.col_keep"), sortable: false },
]);

const nodeItems = computed(() =>
  nodes.value.map((n) => ({
    ...n,
    uptime: formatUptime(n.startedAt),
  })),
);

const roleLabel = (role: string): string => {
  const key = `cluster.roles.${role}`;
  return te(key) ? t(key) : role;
};

const queueLabel = (name: string): string => {
  const key = `cluster.queues_names.${queueLabelKey(name)}`;
  return te(key) ? t(key) : name;
};

const isLeader = (node: ClusterNode): boolean =>
  Boolean(leader.value) && node.id === leader.value;

const onDrain = async (node: ClusterNode): Promise<void> => {
  const ok = await confirm({
    title: t("cluster.drain_title"),
    body: t("cluster.drain_body", { id: node.id }),
    variant: "danger",
    confirmText: t("cluster.drain"),
  });
  if (!ok) return;
  await drain(node);
};

const onPauseToggle = async (queue: QueueView, paused: boolean): Promise<void> => {
  await setPaused(queue, paused);
};

const openKeep = (queue: QueueView): void => {
  keepQueue.value = queue;
  keepOpen.value = true;
};

const onKeepSave = async (keep: string): Promise<void> => {
  if (!keepQueue.value) return;
  const ok = await setKeep(keepQueue.value, keep);
  if (ok) keepOpen.value = false;
};
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-center justify-space-between ga-4 mb-6">
      <div class="min-width-0">
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("cluster.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("cluster.subtitle") }}
        </p>
      </div>
      <v-btn
        v-if="isOwner && !forbidden"
        :color="actionOf('refresh').color"
        :prepend-icon="mdiRefresh"
        :loading="tableLoading || queuesTableLoading"
        class="flex-shrink-0"
        @click="load()"
      >
        {{ t("cluster.refresh") }}
      </v-btn>
    </div>

    <AppCard
      v-if="forbidden"
      class="text-center py-8"
    >
      <v-avatar-hero
        color="warning"
        class="mb-4"
      >
        <v-icon
          :icon="mdiShieldLock"
        />
      </v-avatar-hero>
      <div class="text-title-medium mb-1">
        {{ t("cluster.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("cluster.no_permission_body") }}
      </p>
    </AppCard>

    <template v-else>
      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load()"
          >
            {{ t("cluster.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <div class="d-flex flex-wrap ga-2 mb-4">
        <v-chip
          color="primary"
          label
        >
          {{ t("cluster.totals_nodes", { n: nodeCount }) }}
        </v-chip>
        <v-chip
          color="info"
          label
        >
          {{ t("cluster.totals_accounts", { n: totals.accounts }) }}
        </v-chip>
        <v-chip
          :color="leader ? 'accent' : 'grey'"
          label
        >
          {{ leader ? `${t("cluster.leader")}: ${leader}` : t("cluster.leader_none") }}
        </v-chip>
      </div>

      <h2 class="text-title-medium font-weight-bold mb-3">
        {{ t("cluster.nodes") }}
      </h2>

      <TableCard
        v-model:page="page"
        v-model:ipp="ipp"
        v-model:sort-by="sortBy"
        :headers="nodeHeaders"
        :items="nodeItems"
        :total="nodesTotal"
        :loading="isFirstLoad || tableLoading"
        :searchable="false"
        table-name="cluster-nodes"
        item-value="id"
        class="mb-8"
      >
        <template #no-data>
          <div class="d-flex flex-column align-center text-center py-6 ga-3">
            <v-avatar-hero
              color="primary"
            >
              <v-icon
                :icon="mdiLan"
              />
            </v-avatar-hero>
            <p class="text-body-medium text-medium-emphasis mb-0">
              {{ t("cluster.nodes_empty") }}
            </p>
          </div>
        </template>

        <template #[`item.id`]="{ item }">
          <div class="d-flex align-center ga-2 flex-wrap">
            <span class="font-weight-medium text-numeric">
              {{ item.id }}
            </span>
            <v-chip
              v-if="isLeader(item)"
              color="accent"
              label
            >
              {{ t("cluster.leader_badge") }}
            </v-chip>
          </div>
        </template>

        <template #[`item.roles`]="{ item }">
          <div class="d-flex flex-wrap ga-1">
            <v-chip
              v-for="role in item.roles ?? []"
              :key="role"
              :color="roleColor(role)"
              label
            >
              {{ roleLabel(role) }}
            </v-chip>
          </div>
        </template>

        <template #[`item.uptime`]="{ item }">
          {{ item.uptime }}
        </template>

        <template #[`item.seenAt`]="{ item }">
          {{ formatSeenAt(item.seenAt, locale) }}
        </template>

        <template #[`item.accounts`]="{ item }">
          <span class="text-numeric">
            {{ item.accounts }}
          </span>
        </template>

        <template #[`item.partitions`]="{ item }">
          <span class="text-body-small">
            {{ formatPartitions(item.partitions) }}
          </span>
        </template>

        <template #[`item.status`]="{ item }">
          <v-chip
            v-if="item.draining"
            color="warning"
            label
          >
            {{ t("cluster.draining") }}
          </v-chip>
          <span
            v-else
            class="text-medium-emphasis"
          >
            {{ EMPTY_VALUE }}
          </span>
        </template>

        <template #[`item.actions`]="{ item }">
          <v-btn
            v-if="isOwner && !item.draining"
            :color="actionOf('delete').color"
            :prepend-icon="mdiWaterOutline"
            :disabled="mutating"
            @click.stop="onDrain(item)"
          >
            {{ t("cluster.drain") }}
          </v-btn>
        </template>
      </TableCard>

      <h2 class="text-title-medium font-weight-bold mb-3">
        {{ t("cluster.queues") }}
      </h2>

      <TableCard
        v-model:page="queuePage"
        v-model:ipp="queueIpp"
        v-model:sort-by="queueSortBy"
        :headers="queueHeaders"
        :items="queues"
        :total="queuesTotal"
        :loading="isFirstLoad || queuesTableLoading"
        :searchable="false"
        table-name="queues"
        item-value="name"
      >
        <template #no-data>
          <div class="d-flex flex-column align-center text-center py-6 ga-3">
            <v-avatar-hero
              color="secondary"
            >
              <v-icon
                :icon="mdiLan"
              />
            </v-avatar-hero>
            <p class="text-body-medium text-medium-emphasis mb-0">
              {{ t("cluster.queues_empty") }}
            </p>
          </div>
        </template>

        <template #[`item.name`]="{ item }">
          <div class="d-flex align-center ga-2">
            <div class="min-w-0">
              <div class="font-weight-medium">
                {{ queueLabel(item.name) }}
              </div>
              <div class="text-body-small text-medium-emphasis text-numeric">
                {{ item.name }}
              </div>
            </div>
            <!-- W72: each cancellable queue opens its records on the Queues page. -->
            <v-btn-affix
              v-if="item.name !== 'EMAILBRIDGE_EVENTS'"
              :to="{ path: '/queues', query: { queue: item.name } }"
              :icon="actionOf('open').icon"
              :color="actionOf('open').color"
              :aria-label="t('queues.open_in_queues')"
              :data-testid="`cluster-open-queue-${item.name}`"
              @click.stop
            />
          </div>
        </template>

        <template #[`item.pending`]="{ item }">
          <span class="text-numeric">
            {{ item.pending }}
          </span>
        </template>

        <template #[`item.ackPending`]="{ item }">
          <span class="text-numeric">
            {{ item.ackPending }}
          </span>
        </template>

        <template #[`item.consumers`]="{ item }">
          <span class="text-numeric">
            {{ item.consumers }}
          </span>
        </template>

        <template #[`item.paused`]="{ item }">
          <v-switch
            :model-value="item.paused"
            :aria-label="t('cluster.col_paused')"
            :disabled="!isOwner || mutating"
            :color="actionOf('pause').color"
            class="no-validation ma-0 pa-0"
            hide-details
            @update:model-value="(v) => onPauseToggle(item, Boolean(v))"
          />
        </template>

        <template #[`item.keep`]="{ item }">
          <div class="d-flex align-center ga-2">
            <span class="text-numeric">
              {{ item.keep || EMPTY_VALUE }}
            </span>
            <v-btn-affix
              v-if="isOwner"
              :icon="mdiPencil"
              :color="actionOf('edit').color"
              :aria-label="t('cluster.keep_edit')"
              :disabled="mutating"
              @click.stop="openKeep(item)"
            />
          </div>
        </template>
      </TableCard>
    </template>

    <QueueKeepDialog
      v-model="keepOpen"
      :queue="keepQueue"
      :saving="mutating"
      @save="onKeepSave"
    />
  </div>
</template>
