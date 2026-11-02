<script setup lang="ts">
import { computed, onMounted, ref, watch, type ComponentPublicInstance } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import {
  mdiAlertCircle,
  mdiArrowLeft,
  mdiEmailOutline,
  mdiFlag,
  mdiPaperclip,
  mdiRefresh,
} from "@mdi/js";
import TableCard from "@/components/tables/TableCard.vue";
import { useFillHeight } from "@/composables/useFillHeight";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppCard from "@/components/cards/AppCard.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import FolderTree from "@/features/messages/components/FolderTree.vue";
import BulkActionsBar from "@/features/messages/components/BulkActionsBar.vue";
import MessageReader from "@/features/messages/components/MessageReader.vue";
import MoveDialog from "@/features/messages/components/MoveDialog.vue";
import DraftsPanel from "@/features/messages/components/DraftsPanel.vue";
import { useMessagesBrowser } from "@/features/messages/composables/useMessagesBrowser";
import { useMessageReader } from "@/features/messages/composables/useMessageReader";
import { useDrafts } from "@/features/messages/composables/useDrafts";
import {
  hasAttachmentFlag,
  isFlagged,
  isUnread,
  type FlagFilter,
} from "@/features/messages/helpers/flags";
import { formatBytes, formatFrom, messageDateOf } from "@/features/messages/helpers/format";
import { formatDateTime } from "@/helpers/Date";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import { useSettingsStore } from "@/stores/settings";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import AppDialog from "@/components/AppDialog.vue";


const TABLE_NAME = "messages";
const DEFAULT_VISIBLE = ["select", "from", "subject", "date", "flags", "size"];
const DEFAULT_ORDER = [
  "select",
  "from",
  "subject",
  "date",
  "flags",
  "size",
  "messageIdHeader",
  "uid",
];

const { t } = useI18n();
const { mdAndUp } = useDisplay();
const confirm = useConfirm();
const settings = useSettingsStore();
const refreshAction = actionOf("refresh");
const columnsReady = ref(false);

const {
  accountId,
  mailboxId,
  messageId,
  flagFilter,
  accountOptions,
  accountsLoading,
  mailboxes,
  folderNodes,
  foldersError,
  isFoldersFirstLoad,
  isFoldersRefreshing,
  items,
  total,
  page,
  ipp,
  search,
  sortBy,
  selected,
  bulkBusy,
  loadError,
  empty,
  noResults,
  mobileStep,
  isListFirstLoad,
  isListRefreshing,
  loadAccounts,
  loadFolders,
  loadMessages,
  selectAccount,
  selectFolder,
  openMessage,
  clearMessage,
  backToFolders,
  bulkMarkRead,
  bulkMarkUnread,
  bulkFlag,
  bulkUnflag,
  bulkDelete,
  bulkMove,
} = useMessagesBrowser();

const {
  detail: readerDetail,
  loadError: readerError,
  loadImages,
  rawMissing,
  htmlBody,
  textBody,
  hasHtml,
  hasText,
  attachments,
  attBlocked,
  threadPeers,
  unread: readerUnread,
  flagged: readerFlagged,
  busy: readerBusy,
  downloading,
  isFirstLoad: readerFirstLoad,
  isRefreshing: readerRefreshing,
  show: showReader,
  clear: clearReader,
  toggleSeen,
  toggleFlagged,
  moveTo,
  remove: removeMessage,
  downloadRaw,
  downloadPart,
} = useMessageReader();

const {
  items: draftItems,
  listError: draftsListError,
  detail: draftDetail,
  detailError: draftDetailError,
  open: draftOpen,
  isListFirstLoad: draftsFirstLoad,
  isListRefreshing: draftsRefreshing,
  isDetailFirstLoad: draftDetailFirstLoad,
  isDetailRefreshing: draftDetailRefreshing,
  load: loadDrafts,
  show: showDraft,
  close: closeDraft,
} = useDrafts();

const moveOpen = ref(false);
const moveMode = ref<"bulk" | "one">("bulk");
const draftDetailOpen = computed({
  get: () => draftOpen.value,
  set: (v: boolean) => {
    if (!v) closeDraft();
  },
});

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({
    title: `${a.name} · ${a.email}`,
    value: a.id,
  })),
);

const flagItems = computed(() => [
  { title: t("messages.filter_all"), value: "" },
  { title: t("messages.filter_unread"), value: "unread" },
  { title: t("messages.filter_flagged"), value: "flagged" },
]);

const headers = computed<ServerTableHeader[]>(() => [
  { key: "select", title: t("messages.col_select"), sortable: false, width: "48px", forced: true },
  { key: "from", title: t("messages.col_from"), sortable: false },
  { key: "subject", title: t("messages.col_subject"), sortable: false },
  { key: "date", title: t("messages.col_date"), sortable: true },
  { key: "flags", title: t("messages.col_flags"), sortable: false, width: "96px" },
  { key: "size", title: t("messages.col_size"), sortable: true },
  { key: "messageIdHeader", title: t("messages.col_message_id"), sortable: false },
  { key: "uid", title: t("messages.col_uid"), sortable: true },
]);

const tableItems = computed(() =>
  items.value.map((row) => ({
    ...row,
    fromLabel: formatFrom(row.from),
    subjectLabel: row.subject || t("messages.no_subject"),
    dateLabel: formatDateTime(messageDateOf(row)),
    _unread: isUnread(row.flags),
    _flagged: isFlagged(row.flags),
    _attach: hasAttachmentFlag(row.flags),
  })),
);

const showFoldersPane = computed(
  () => mdAndUp.value || mobileStep.value === "folders",
);
const showListPane = computed(
  () => mdAndUp.value || mobileStep.value === "list",
);
const showReaderPane = computed(
  () => Boolean(messageId.value) && (mdAndUp.value || mobileStep.value === "reader"),
);
// The cards around the table end where the table card ends — at the viewport bottom — and
// scroll inside (a fixed height also replaces AppCard's 100 % of the stretched column, which
// overflowed by the fields above the card). The reader on a phone shows alone and keeps its
// natural height.
const foldersCard = ref<ComponentPublicInstance | null>(null);
const readerCard = ref<ComponentPublicInstance | null>(null);
const emptyCard = ref<ComponentPublicInstance | null>(null);
const cardEl = (card: ComponentPublicInstance | null): HTMLElement | null =>
  card?.$el instanceof HTMLElement ? card.$el : null;
const foldersFill = useFillHeight(() => cardEl(foldersCard.value));
const readerFill = useFillHeight(() => cardEl(readerCard.value), { enabled: () => mdAndUp.value });
const emptyFill = useFillHeight(() => cardEl(emptyCard.value));
const fillStyle = (fill: number | null): Record<string, string> | undefined =>
  fill == null ? undefined : { height: `${fill}px` };

const listColsMd = computed(() => (messageId.value && mdAndUp.value ? 4 : 9));

onMounted(async () => {
  await settings.load();
  if (!settings.getTable(TABLE_NAME)?.visibility) {
    settings.setVisibility(TABLE_NAME, DEFAULT_VISIBLE, DEFAULT_ORDER);
  }
  columnsReady.value = true;

  const accounts = await loadAccounts();
  if (!accountId.value && accounts.length) {
    // Prefer last-used account from the query/session; else the first mailbox.
    let last = "";
    try {
      last = localStorage.getItem("eb.messages.lastAccountId") ?? "";
    } catch {
      /* ignore */
    }
    const pick =
      (last && accounts.some((a) => a.id === last) ? last : "") ||
      accounts[0]?.id ||
      "";
    if (pick) await selectAccount(pick);
  }
  if (accountId.value) {
    await loadFolders();
    void loadDrafts(accountId.value);
    if (mailboxId.value) await loadMessages();
    if (messageId.value && mailboxId.value) {
      await showReader(accountId.value, mailboxId.value, messageId.value);
    }
  }
});

watch(accountId, (id) => {
  if (!id) return;
  try {
    localStorage.setItem("eb.messages.lastAccountId", id);
  } catch {
    /* ignore */
  }
});

watch(messageId, (id) => {
  if (id && accountId.value && mailboxId.value) {
    void showReader(accountId.value, mailboxId.value, id);
  } else {
    clearReader();
  }
});

const onAccountChange = async (id: unknown): Promise<void> => {
  const next = typeof id === "string" ? id : "";
  if (!next) {
    accountId.value = "";
    return;
  }
  await selectAccount(next);
  void loadDrafts(next);
};

const onFolderSelect = (id: string): void => {
  selectFolder(id);
};

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const id = String(row.item.id ?? "");
  if (id) openMessage(id);
};

const toggleSelect = (id: string): void => {
  const i = selected.value.indexOf(id);
  if (i >= 0) selected.value = selected.value.filter((x) => x !== id);
  else selected.value = [...selected.value, id];
};

const onFlagFilter = (v: unknown): void => {
  flagFilter.value = (typeof v === "string" ? v : "") as FlagFilter;
  page.value = 1;
};

const openMoveBulk = (): void => {
  moveMode.value = "bulk";
  moveOpen.value = true;
};

const openMoveOne = (): void => {
  moveMode.value = "one";
  moveOpen.value = true;
};

const onMoveConfirm = async (targetId: string): Promise<void> => {
  if (moveMode.value === "bulk") {
    await bulkMove(targetId);
  } else {
    const ok = await moveTo(targetId);
    if (ok) {
      clearMessage();
      void loadMessages();
    }
  }
};

const onBulkDelete = async (): Promise<void> => {
  const ok = await confirm({
    title: t("messages.delete_title"),
    body: t("messages.delete_body"),
    variant: "danger",
    confirmText: t("messages.delete_confirm"),
  });
  if (!ok) return;
  await bulkDelete();
};

const onReaderDelete = async (): Promise<void> => {
  const ok = await confirm({
    title: t("messages.delete_title"),
    body: t("messages.delete_body"),
    variant: "danger",
    confirmText: t("messages.delete_confirm"),
  });
  if (!ok) return;
  const removed = await removeMessage();
  if (removed) {
    clearMessage();
    void loadMessages();
  }
};

const onOpenDraft = (draftId: string): void => {
  if (!accountId.value) return;
  void showDraft(accountId.value, draftId);
};

const onOpenThread = (id: string): void => {
  openMessage(id);
};
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t("messages.title") }}
      </h1>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("messages.subtitle") }}
      </p>
    </div>

    <v-alert
      v-if="!accountId"
      :prepend-icon="mdiEmailOutline"
      type="info"
    >
      {{ t("messages.pick_account") }}
    </v-alert>

    <v-row
      v-else
    >
      <!-- Folders -->
      <v-col
        v-show="showFoldersPane"
        cols="12"
        md="3"
        lg="3"
      >
        <AutocompleteSearch
          :model-value="accountId"
          :items="accountItems"
          :loading="accountsLoading"
          :label="t('messages.account')"
          :placeholder="t('messages.account_placeholder')"
          class="no-validation mb-3"
          server-search
          clearable
          hide-details
          @update:search="loadAccounts"
          @update:model-value="onAccountChange"
        />

        <AppCard
          ref="foldersCard"
          :style="fillStyle(foldersFill)"
          class="pa-4 overflow-y-auto"
        >
          <FolderTree
            :nodes="folderNodes"
            :selected-id="mailboxId"
            :is-first-load="isFoldersFirstLoad"
            :is-refreshing="isFoldersRefreshing"
            :error="foldersError"
            :empty="!isFoldersFirstLoad && !foldersError && !folderNodes.length"
            @select="onFolderSelect"
          />
          <DraftsPanel
            :items="draftItems"
            :is-first-load="draftsFirstLoad"
            :is-refreshing="draftsRefreshing"
            :error="draftsListError"
            @open="onOpenDraft"
          />
        </AppCard>
      </v-col>

      <!-- List -->
      <v-col
        v-show="showListPane"
        :md="listColsMd"
        :lg="listColsMd"
        cols="12"
      >
        <div
          v-if="!mdAndUp && mailboxId"
          class="mb-3"
        >
          <v-btn-inline
            :prepend-icon="mdiArrowLeft"
            color="primary"
            @click="backToFolders"
          >
            {{ t("messages.back_folders") }}
          </v-btn-inline>
        </div>

        <v-alert
          v-if="!mailboxId"
          type="info"
        >
          {{ t("messages.folder_pick") }}
        </v-alert>

        <template v-else>
          <div class="d-flex flex-wrap align-center ga-2 mb-3">
            <AutocompleteSearch
              :model-value="flagFilter"
              :items="flagItems"
              :label="t('messages.flags')"
              class="no-validation"
              style="max-width: 200px"
              hide-details
              @update:model-value="onFlagFilter"
            />
            <v-spacer />
            <v-btn
              :prepend-icon="mdiRefresh"
              :color="refreshAction.color"
              @click="loadMessages"
            >
              {{ t("messages.refresh") }}
            </v-btn>
          </div>

          <BulkActionsBar
            :count="selected.length"
            :busy="bulkBusy"
            @read="bulkMarkRead"
            @unread="bulkMarkUnread"
            @flag="bulkFlag"
            @unflag="bulkUnflag"
            @move="openMoveBulk"
            @delete="onBulkDelete"
          />

          <v-alert
            v-if="loadError"
            :prepend-icon="mdiAlertCircle"
            class="mb-3"
            type="error"
          >
            {{ loadError }}
          </v-alert>

          <AppCard
            v-if="empty || noResults"
            ref="emptyCard"
            :style="fillStyle(emptyFill)"
            class="pa-8 text-center"
          >
            <div class="text-body-large text-medium-emphasis">
              {{ empty ? t("messages.empty") : t("messages.no_results") }}
            </div>
          </AppCard>

          <TableCard
            v-else-if="columnsReady"
            :key="TABLE_NAME"
            v-model:page="page"
            v-model:ipp="ipp"
            v-model:sort-by="sortBy"
            v-model:search="search"
            :headers="headers"
            :items="tableItems"
            :total="total"
            :loading="isListFirstLoad || isListRefreshing"
            :table-name="TABLE_NAME"
            item-value="id"
            @click:row="onRowClick"
          >
            <template #card="{ item, clickable, select }">
              <v-card
                :link="clickable"
                class="d-flex flex-column h-100"
                @click="select"
              >
                <v-card-text class="d-flex ga-3 align-start">
                  <v-checkbox
                    :model-value="selected.includes(String(item.id))"
                    class="no-validation flex-grow-0 mt-0"
                    color="primary"
                    hide-details
                    @click.stop
                    @update:model-value="toggleSelect(String(item.id))"
                  />
                  <div class="flex-grow-1 min-w-0">
                    <div class="d-flex align-center ga-2 mb-1">
                      <span
                        v-if="item._unread"
                        class="messages-unread-dot flex-grow-0"
                        aria-hidden="true"
                      />
                      <span class="text-body-medium text-truncate">
                        {{ item.fromLabel }}
                      </span>
                      <span class="text-body-small text-medium-emphasis ms-auto flex-grow-0">
                        {{ item.dateLabel }}
                      </span>
                    </div>
                    <div
                      :class="{ 'font-weight-bold': item._unread }"
                      class="text-body-medium text-truncate"
                    >
                      {{ item.subjectLabel }}
                    </div>
                    <div class="d-flex align-center ga-2 mt-1">
                      <v-icon-inline
                        v-if="item._flagged"
                        v-tippy="t('messages.flag_flagged')"
                        :icon="mdiFlag"
                        color="warning"
                      />
                      <v-icon-inline
                        v-if="item._attach"
                        :icon="mdiPaperclip"
                        color="secondary"
                      />
                      <span class="text-body-small text-medium-emphasis">
                        {{ formatBytes(Number(item.size)) }}
                      </span>
                    </div>
                  </div>
                </v-card-text>
              </v-card>
            </template>
            <template #[`item.select`]="{ item }">
              <v-checkbox
                :model-value="selected.includes(String(item.id))"
                class="no-validation"
                color="primary"
                hide-details
                @click.stop
                @update:model-value="toggleSelect(String(item.id))"
              />
            </template>
            <template #[`item.from`]="{ item }">
              <span :class="{ 'font-weight-bold': item._unread }">
                {{ item.fromLabel }}
              </span>
            </template>
            <template #[`item.subject`]="{ item }">
              <span
                :class="{ 'font-weight-bold': item._unread }"
                class="text-truncate d-inline-block"
                style="max-width: 320px"
              >
                {{ item.subjectLabel }}
              </span>
            </template>
            <template #[`item.date`]="{ item }">
              {{ item.dateLabel }}
            </template>
            <template #[`item.flags`]="{ item }">
              <div class="d-flex align-center ga-1">
                <span
                  v-if="item._unread"
                  v-tippy="t('messages.flag_unread')"
                  :aria-label="t('messages.flag_unread')"
                  class="messages-unread-dot"
                  role="img"
                />
                <v-icon-inline
                  v-if="item._flagged"
                  v-tippy="t('messages.flag_flagged')"
                  :icon="mdiFlag"
                  color="warning"
                />
                <v-icon-inline
                  v-if="item._attach"
                  :icon="mdiPaperclip"
                  color="secondary"
                />
              </div>
            </template>
            <template #[`item.messageIdHeader`]="{ item }">
              <span
                class="text-body-small text-truncate d-inline-block"
                style="max-width: 220px"
              >
                {{ item.messageIdHeader || item.emailId || "—" }}
              </span>
            </template>
            <template #[`item.size`]="{ item }">
              {{ formatBytes(Number(item.size)) }}
            </template>
          </TableCard>
        </template>
      </v-col>

      <!-- Reader -->
      <v-col
        v-if="showReaderPane"
        cols="12"
        md="5"
        lg="5"
      >
        <AppCard
          ref="readerCard"
          :style="fillStyle(readerFill)"
          class="pa-4 overflow-y-auto"
        >
          <MessageReader
            v-model:load-images="loadImages"
            :detail="readerDetail"
            :load-error="readerError"
            :raw-missing="rawMissing"
            :html-body="htmlBody"
            :text-body="textBody"
            :has-html="hasHtml"
            :has-text="hasText"
            :attachments="attachments"
            :att-blocked="attBlocked"
            :thread-peers="threadPeers"
            :unread="readerUnread"
            :flagged="readerFlagged"
            :busy="readerBusy"
            :downloading="downloading"
            :is-first-load="readerFirstLoad"
            :is-refreshing="readerRefreshing"
            :show-back="!mdAndUp"
            @back="clearMessage"
            @toggle-seen="toggleSeen"
            @toggle-flagged="toggleFlagged"
            @move="openMoveOne"
            @delete="onReaderDelete"
            @download-raw="downloadRaw"
            @download-part="downloadPart"
            @open-thread="onOpenThread"
          />
        </AppCard>
      </v-col>
    </v-row>

    <MoveDialog
      v-model="moveOpen"
      :mailboxes="mailboxes"
      :exclude-id="mailboxId"
      @confirm="onMoveConfirm"
    />

    <AppDialog
      v-model="draftDetailOpen"
      :title="t('messages.draft_title')"
      :max-width="720"
    >
      <AppSectionLoader :status="draftDetailRefreshing" />
      <LoadingSkeleton
        v-if="draftDetailFirstLoad"
        :rows="4"
        shape="form"
      />
      <v-alert
        v-else-if="draftDetailError"
        type="error"
      >
        {{ draftDetailError }}
      </v-alert>
      <template v-else-if="draftDetail">
        <v-alert
          class="mb-3"
          type="info"
        >
          {{ t("messages.draft_readonly") }}
        </v-alert>
        <div class="text-body-large font-weight-medium mb-2">
          {{ draftDetail.subject || t("messages.no_subject") }}
        </div>
        <pre class="text-body-small pa-3 rounded-lg messages-draft-text">{{
              draftDetail.text || draftDetail.html || "—"
        }}</pre>
      </template>

      <template #footer>
        <v-btn
          @click="closeDraft()"
        >
          {{ t("messages.back") }}
        </v-btn>
      </template>
    </AppDialog>
  </div>
</template>

<style scoped lang="scss">
.messages-draft-text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  max-height: 360px;
  overflow: auto;
}

.messages-unread-dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: rgb(var(--v-theme-primary));
  flex-shrink: 0;
}
</style>
