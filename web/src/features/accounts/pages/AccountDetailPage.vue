<script setup lang="ts">
import { computed, onMounted, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import {
  mdiDelete,
  mdiDownload,
  mdiLanConnect,
  mdiPencil,
  mdiPlayCircle,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { OutboundStates } from "@/constants/Statuses";
import { useConfirm } from "@/composables/useConfirm";
import { useSwitchLabels } from "@/composables/useSwitchLabels";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import AccountEventsPanel from "@/features/accounts/components/AccountEventsPanel.vue";
import AccountFoldersPanel from "@/features/accounts/components/AccountFoldersPanel.vue";
import AccountMetadataEditor from "@/features/accounts/components/AccountMetadataEditor.vue";
import AccountOAuthPanel from "@/features/accounts/components/AccountOAuthPanel.vue";
import AccountRoutingPanel from "@/features/accounts/components/AccountRoutingPanel.vue";
import AccountStateChip from "@/features/accounts/components/AccountStateChip.vue";
import AccountSyncButton from "@/features/accounts/components/AccountSyncButton.vue";
import { useAccountDetail } from "@/features/accounts/composables/useAccountDetail";
import {
  canDelete,
  canEdit,
  canReconnect,
  canResume,
  canSync,
  isOrgMasterSwitch,
  type AccountSwitchKey,
} from "@/features/accounts/helpers/accountState";
import AccountExportsPanel from "@/features/exports/components/AccountExportsPanel.vue";
import AccountHealthPanel from "@/features/health/components/AccountHealthPanel.vue";
import AccountPecPanel from "@/features/pec/components/AccountPecPanel.vue";
import FullTextReindexButton from "@/features/search/components/FullTextReindexButton.vue";
import SemanticReindexButton from "@/features/search/components/SemanticReindexButton.vue";
import { fullTextSwitchOf } from "@/features/search/helpers/api";

const { t, te } = useI18n();
const { switchLabel } = useSwitchLabels();
const router = useRouter();
const confirm = useConfirm();

const {
  account,
  loadError,
  mutating,
  tab,
  isFirstLoad,
  isRefreshing,
  recentErrors,
  outboxItems,
  outboxTotal,
  outboxPage,
  outboxIpp,
  outboxSortBy,
  outboxError,
  outboxLoading,
  logItems,
  logCursor,
  logProto,
  logFrom,
  logTo,
  logError,
  logDownloading,
  logsFirstLoad,
  logsRefreshing,
  switchesSaving,
  switchKeys,
  storedMetadata,
  metadataRows,
  metadataErrors,
  metadataSaving,
  metadataDirty,
  metadataValid,
  resetMetadata,
  saveMetadata,
  load,
  loadOutbox,
  loadLogs,
  doSync,
  doReconnect,
  doResume,
  doDelete,
  setSwitch,
  isSwitchOn,
  downloadLogs,
} = useAccountDetail();

onMounted(() => {
  void load();
});

watch(tab, (v) => {
  if (v === "outbox") void loadOutbox();
  if (v === "logs") void loadLogs();
});

watch([outboxPage, outboxIpp, outboxSortBy], () => {
  if (tab.value === "outbox") void loadOutbox();
});

watch([logProto, logFrom, logTo], () => {
  if (tab.value === "logs") void loadLogs(false);
});

const editAction = actionOf("edit");
const deleteAction = actionOf("delete");
const reconnectAction = actionOf("reconnect");
const resumeAction = actionOf("resume");
const downloadAction = actionOf("download");
const saveAction = actionOf("save");
const cancelAction = actionOf("cancel");

const metadataEntries = computed(() =>
  Object.keys(storedMetadata.value)
    .sort((a, b) => a.localeCompare(b))
    .map((key) => ({ key, value: storedMetadata.value[key] ?? null })),
);

const metadataDisplay = (v: string | number | boolean | null): string =>
  v === null ? "null" : v === "" ? EMPTY_VALUE : String(v);

const outboxHeaders = computed<ServerTableHeader[]>(() => [
  { key: "createdAt", title: t("accounts.outbox_col_created"), sortable: true },
  { key: "to", title: t("accounts.outbox_col_to"), sortable: false },
  { key: "subject", title: t("accounts.outbox_col_subject"), sortable: false },
  { key: "state", title: t("accounts.outbox_col_state"), sortable: true },
  { key: "attempts", title: t("accounts.outbox_col_attempts"), sortable: false },
]);

const protoItems = computed(() => [
  { title: t("accounts.logs_all_protos"), value: "" },
  { title: t("accounts.proto_imap"), value: "imap" },
  { title: t("accounts.proto_smtp"), value: "smtp" },
]);

const formatWhen = (iso?: string): string =>
  iso ? DateUtil.formatDateTime(iso) : EMPTY_VALUE;

const formatLogTime = (ms: number): string =>
  DateUtil.formatDateTime(new Date(ms).toISOString());

const outboundTo = (item: { envelope?: { to?: string[] | null } }): string => {
  const to = item.envelope?.to;
  if (!to?.length) return EMPTY_VALUE;
  if (to.length === 1) return to[0]!;
  return `${to[0]} +${to.length - 1}`;
};

const outboundStateColor = (state: string): string =>
  (OutboundStates as Record<string, { color: string }>)[state]?.color ?? "grey";

const outboundStateLabel = (state: string): string => {
  const key = `accounts.outbound_${state}`;
  return te(key) ? t(key) : state;
};

const onDelete = async (): Promise<void> => {
  const ok = await confirm({
    title: t("accounts.delete_title"),
    body: t("accounts.delete_body"),
    variant: "danger",
    confirmText: t("accounts.action_delete"),
  });
  if (!ok) return;
  await doDelete();
};

const onSwitch = (key: AccountSwitchKey, value: boolean | null): void => {
  void setSwitch(key, Boolean(value));
};

const pecEnabled = computed(
  () => account.value?.switches?.["pec.enabled"] === true,
);
</script>

<template>
  <div>
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
      shape="form"
    />

    <div
      v-else
      class="position-relative"
    >
      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load"
          >
            {{ t("accounts.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <template v-else-if="account">
        <div class="d-flex flex-wrap align-start justify-space-between ga-4 mb-6">
          <div class="min-w-0">
            <div class="d-flex flex-wrap align-center ga-3 mb-1">
              <h1 class="text-h5 font-weight-bold mb-0">
                {{ account.name }}
              </h1>
              <AccountStateChip :state="account.state" />
            </div>
            <p class="text-body-medium text-medium-emphasis mb-0">
              {{ account.email }}
            </p>
          </div>
          <div class="d-flex flex-wrap ga-2">
            <AccountSyncButton
              v-if="canSync(account.state)"
              :loading="mutating"
              labelled
              @sync="(mode) => doSync(mode)"
            />
            <v-btn
              v-if="canReconnect(account.state)"
              :loading="mutating"
              :color="reconnectAction.color"
              :prepend-icon="mdiLanConnect"
              @click="doReconnect"
            >
              {{ t("accounts.action_reconnect") }}
            </v-btn>
            <v-btn
              v-if="canResume(account.state)"
              :loading="mutating"
              :color="resumeAction.color"
              :prepend-icon="mdiPlayCircle"
              @click="doResume"
            >
              {{ t("accounts.action_resume") }}
            </v-btn>
            <v-btn
              v-if="canEdit(account.state)"
              :color="editAction.color"
              :prepend-icon="mdiPencil"
              :to="`/accounts/${account.id}/edit`"
            >
              {{ t("accounts.action_edit") }}
            </v-btn>
            <v-btn
              v-if="canDelete(account.state)"
              :loading="mutating"
              :color="deleteAction.color"
              :prepend-icon="mdiDelete"
              @click="onDelete"
            >
              {{ t("accounts.action_delete") }}
            </v-btn>
          </div>
        </div>

        <v-tabs
          v-model="tab"
          color="primary"
          class="mb-4"
        >
          <v-tab value="overview">
            {{ t("accounts.tab_overview") }}
          </v-tab>
          <v-tab value="oauth">
            {{ t("accounts.tab_oauth") }}
          </v-tab>
          <v-tab value="folders">
            {{ t("accounts.tab_folders") }}
          </v-tab>
          <v-tab value="events">
            {{ t("accounts.tab_events") }}
          </v-tab>
          <v-tab value="outbox">
            {{ t("accounts.tab_outbox") }}
          </v-tab>
          <v-tab value="logs">
            {{ t("accounts.tab_logs") }}
          </v-tab>
          <v-tab value="routing">
            {{ t("accounts.tab_routing") }}
          </v-tab>
          <v-tab value="settings">
            {{ t("accounts.tab_settings") }}
          </v-tab>
          <v-tab value="exports">
            {{ t("exports.tab") }}
          </v-tab>
          <v-tab
            v-if="pecEnabled"
            value="pec"
          >
            {{ t("pec.tab") }}
          </v-tab>
        </v-tabs>

        <v-tabs-window
          v-model="tab"
          class="account-tabs-window"
        >
          <v-tabs-window-item value="overview">
            <div class="position-relative">
              <v-row>
                <v-col cols="12">
                  <AccountHealthPanel
                    :account-id="account.id"
                    class="mb-4"
                  />
                </v-col>
                <v-col
                  cols="12"
                  md="6"
                >
                  <AppCard class="mb-4">
                    <template #header>
                      <span class="text-title-medium font-weight-bold">
                        {{ t("accounts.overview_imap") }} / {{ t("accounts.overview_smtp") }}
                      </span>
                    </template>
                    <div class="text-body-medium mb-2">
                      IMAP {{ account.imap.host }}:{{ account.imap.port }} ({{ account.imap.tls }})
                    </div>
                    <div class="text-body-medium">
                      SMTP {{ account.smtp.host }}:{{ account.smtp.port }}
                      · {{ account.smtp.policy || "standard" }}
                    </div>
                  </AppCard>
                </v-col>
                <v-col
                  v-if="metadataEntries.length"
                  cols="12"
                  md="6"
                >
                  <AppCard
                    class="mb-4"
                    data-testid="overview-metadata"
                  >
                    <template #header>
                      <span class="text-title-medium font-weight-bold">
                        {{ t("accounts.metadata_title") }}
                      </span>
                    </template>
                    <div class="d-flex flex-wrap ga-2">
                      <v-chip
                        v-for="entry in metadataEntries"
                        :key="entry.key"
                        color="info"
                        label
                      >
                        <span class="font-weight-medium">{{ entry.key }}</span>
                        <span class="text-medium-emphasis mx-1">:</span>
                        <span class="text-truncate">{{ metadataDisplay(entry.value) }}</span>
                      </v-chip>
                    </div>
                  </AppCard>
                </v-col>
                <v-col cols="12">
                  <AppCard>
                    <template #header>
                      <span class="text-title-medium font-weight-bold">
                        {{ t("accounts.overview_errors") }}
                      </span>
                    </template>
                    <div
                      v-if="!recentErrors.length"
                      class="text-body-medium text-medium-emphasis"
                    >
                      {{ t("accounts.overview_errors_empty") }}
                    </div>
                    <v-timeline
                      v-else
                      side="end"
                    >
                      <v-timeline-item
                        v-for="(err, idx) in recentErrors"
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
                          v-if="err.message"
                          class="text-body-small"
                        >
                          {{ err.message }}
                        </div>
                      </v-timeline-item>
                    </v-timeline>
                  </AppCard>
                </v-col>
              </v-row>
              <AppSectionLoader :status="isRefreshing" />
            </div>
          </v-tabs-window-item>

          <v-tabs-window-item value="oauth">
            <AccountOAuthPanel
              v-if="account?.id"
              :account-id="account.id"
              :provider="account.provider"
              :token-export-enabled="account.switches?.['oauth.tokenExport'] === true"
              can-export-token
              @changed="load"
            />
          </v-tabs-window-item>

          <v-tabs-window-item value="folders">
            <AccountFoldersPanel
              v-if="account?.id"
              :account-id="account.id"
            />
          </v-tabs-window-item>

          <v-tabs-window-item value="events">
            <AccountEventsPanel
              v-if="account?.id"
              :account-id="account.id"
            />
          </v-tabs-window-item>

          <v-tabs-window-item value="outbox">
            <v-alert
              v-if="outboxError"
              type="error"
              class="mb-4"
            >
              {{ outboxError }}
            </v-alert>
            <TableCard
              v-else
              v-model:page="outboxPage"
              v-model:ipp="outboxIpp"
              v-model:sort-by="outboxSortBy"
              :headers="outboxHeaders"
              :items="outboxItems"
              :total="outboxTotal"
              :loading="outboxLoading"
              :searchable="false"
              table-name="account-outbox"
              item-value="id"
              @click:row="(_e, row) => router.push(`/outbox/${row.item.id}`)"
            >
              <template #[`item.createdAt`]="{ item }">
                {{ formatWhen(item.createdAt) }}
              </template>
              <template #[`item.to`]="{ item }">
                {{ outboundTo(item) }}
              </template>
              <template #[`item.subject`]="{ item }">
                {{ item.subject || EMPTY_VALUE }}
              </template>
              <template #[`item.state`]="{ item }">
                <v-chip
                  :color="outboundStateColor(item.state)"
                  label
                >
                  {{ outboundStateLabel(item.state) }}
                </v-chip>
              </template>
              <template #[`item.attempts`]="{ item }">
                {{ (item.attempts?.length ?? 0) }}/{{ item.maxAttempts }}
              </template>
              <template #no-data>
                <div class="text-center py-6 text-medium-emphasis">
                  {{ t("accounts.outbox_empty") }}
                </div>
              </template>
            </TableCard>
          </v-tabs-window-item>

          <v-tabs-window-item value="routing">
            <AccountRoutingPanel
              v-if="account?.id"
              :account-id="account.id"
            />
          </v-tabs-window-item>

          <v-tabs-window-item value="logs">
            <AppCard class="mb-4 h-auto">
              <div class="d-flex flex-wrap align-center ga-4 mb-4">
                <v-switch
                  :model-value="isSwitchOn('protocolLogs.enabled')"
                  :label="t('accounts.logs_enable')"
                  :loading="switchesSaving"
                  :hint="t('accounts.logs_enable_hint')"
                  color="info"
                  class="no-validation"
                  persistent-hint
                  @update:model-value="(v) => onSwitch('protocolLogs.enabled', v)"
                />
                <v-switch
                  :model-value="isSwitchOn('protocolLogs.rawDebug')"
                  :label="t('accounts.logs_raw')"
                  :loading="switchesSaving"
                  :hint="t('accounts.logs_raw_hint')"
                  color="warning"
                  class="no-validation"
                  persistent-hint
                  @update:model-value="(v) => onSwitch('protocolLogs.rawDebug', v)"
                />
              </div>
              <v-row
                class="align-center"
              >
                <v-col
                  cols="12"
                  sm="4"
                  md="3"
                >
                  <v-select
                    v-model="logProto"
                    :items="protoItems"
                    :label="t('accounts.logs_proto')"
                    class="no-validation"
                    hide-details
                  />
                </v-col>
                <v-col
                  cols="12"
                  sm="4"
                  md="3"
                >
                  <v-text-field
                    v-model="logFrom"
                    :label="t('accounts.logs_from')"
                    type="datetime-local"
                    class="no-validation"
                    hide-details
                  />
                </v-col>
                <v-col
                  cols="12"
                  sm="4"
                  md="3"
                >
                  <v-text-field
                    v-model="logTo"
                    :label="t('accounts.logs_to')"
                    type="datetime-local"
                    class="no-validation"
                    hide-details
                  />
                </v-col>
                <v-col
                  cols="12"
                  md="3"
                  class="d-flex justify-md-end"
                >
                  <v-btn
                    :loading="logDownloading"
                    :color="downloadAction.color"
                    :prepend-icon="mdiDownload"
                    @click="downloadLogs"
                  >
                    {{ t("accounts.logs_download") }}
                  </v-btn>
                </v-col>
              </v-row>
            </AppCard>

            <LoadingSkeleton
              v-if="logsFirstLoad"
              :rows="6"
              shape="list"
            />
            <template v-else>
              <v-alert
                v-if="logError"
                type="error"
                class="mb-4"
              >
                {{ logError }}
              </v-alert>
              <AppCard
                v-else
                class="h-auto"
              >
                <v-progress-linear
                  v-if="logsRefreshing"
                  color="info"
                  class="mb-2"
                  indeterminate
                />
                <div
                  v-if="!logItems.length"
                  class="text-body-medium text-medium-emphasis py-4"
                >
                  {{ t("accounts.logs_empty") }}
                </div>
                <div
                  v-else
                  class="font-mono text-body-small"
                  style="max-height: 480px; overflow: auto"
                >
                  <div
                    v-for="line in logItems"
                    :key="line.id"
                    class="d-flex ga-2 py-1 border-b"
                  >
                    <span class="text-medium-emphasis text-no-wrap">
                      {{ formatLogTime(line.time) }}
                    </span>
                    <v-chip
                      :color="line.proto === 'imap' ? 'info' : 'secondary'"
                      label
                    >
                      {{ line.proto }}
                    </v-chip>
                    <span class="text-medium-emphasis">{{ line.dir }}</span>
                    <span class="text-wrap">{{ line.line }}</span>
                  </div>
                </div>
                <div
                  v-if="logCursor"
                  class="mt-4"
                >
                  <v-btn
                    color="secondary"
                    @click="loadLogs(true)"
                  >
                    {{ t("accounts.logs_load_more") }}
                  </v-btn>
                </div>
              </AppCard>
            </template>
          </v-tabs-window-item>

          <v-tabs-window-item value="exports">
            <AccountExportsPanel
              v-if="account?.id"
              :account-id="account.id"
            />
          </v-tabs-window-item>

          <v-tabs-window-item
            v-if="pecEnabled"
            value="pec"
          >
            <AccountPecPanel
              v-if="account?.id"
              :account-id="account.id"
            />
          </v-tabs-window-item>

          <v-tabs-window-item value="settings">
            <div class="position-relative">
              <AppCard class="h-auto">
                <template #header>
                  <span class="text-title-medium font-weight-bold">
                    {{ t("accounts.settings_title") }}
                  </span>
                </template>
                <div
                  v-for="key in switchKeys"
                  :key="key"
                  class="mb-2"
                >
                  <v-switch
                    :model-value="isSwitchOn(key)"
                    :label="switchLabel(key)"
                    :loading="switchesSaving"
                    :hint="isOrgMasterSwitch(key) ? t('accounts.switch_follows_org') : undefined"
                    :persistent-hint="isOrgMasterSwitch(key)"
                    color="primary"
                    class="no-validation"
                    @update:model-value="(v) => onSwitch(key, v)"
                  />
                </div>
                <FullTextReindexButton
                  v-if="account?.id"
                  :account-id="account.id"
                  :opted-out="fullTextSwitchOf(account.switches) === false"
                />
                <SemanticReindexButton
                  v-if="account?.id"
                  :account-id="account.id"
                  :revision="account.version"
                />
              </AppCard>
              <AppCard
                class="mt-4 h-auto"
                data-testid="settings-metadata"
              >
                <AccountMetadataEditor
                  v-model="metadataRows"
                  :server-errors="metadataErrors"
                  :disabled="metadataSaving"
                />
                <div class="d-flex flex-wrap justify-end ga-2 mt-4">
                  <v-btn
                    :color="cancelAction.color"
                    :disabled="!metadataDirty || metadataSaving"
                    @click="resetMetadata"
                  >
                    {{ t("accounts.metadata_reset") }}
                  </v-btn>
                  <v-btn
                    :color="saveAction.color"
                    :prepend-icon="saveAction.icon"
                    :loading="metadataSaving"
                    :disabled="!metadataDirty || !metadataValid"
                    data-testid="metadata-save"
                    @click="saveMetadata"
                  >
                    {{ t("accounts.metadata_save") }}
                  </v-btn>
                </div>
              </AppCard>
              <AppSectionLoader :status="isRefreshing" />
            </div>
          </v-tabs-window-item>
        </v-tabs-window>
      </template>
    </div>
  </div>
</template>

<style scoped lang="scss">
// The global `.v-window { overflow: visible }` keeps shadows and focus rings; clip only the
// horizontal axis so the sliding tab transition never widens the page.
.account-tabs-window {
  overflow-x: clip;
}
</style>
