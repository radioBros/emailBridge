<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiArrowLeft,
  mdiDelete,
  mdiDownload,
  mdiEmailCheck,
  mdiEmailMarkAsUnread,
  mdiFlag,
  mdiFlagOff,
  mdiFolderMove,
} from "@mdi/js";
import type { AttachmentView, MessageView } from "@/api/messages";
import SandboxedHtml from "@/features/sandbox/components/SandboxedHtml.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import { formatAddresses, formatBytes, headerEntries } from "@/features/messages/helpers/format";

const props = defineProps<{
  detail: MessageView | null;
  loadError: string | null;
  loadImages: boolean;
  rawMissing: boolean;
  htmlBody: string;
  textBody: string;
  hasHtml: boolean;
  hasText: boolean;
  attachments: AttachmentView[];
  attBlocked?: string | null;
  threadPeers: MessageView[];
  unread: boolean;
  flagged: boolean;
  busy: boolean;
  downloading: boolean;
  isFirstLoad: boolean;
  isRefreshing: boolean;
  showBack?: boolean;
}>();

const emit = defineEmits<{
  back: [];
  "update:loadImages": [value: boolean];
  toggleSeen: [];
  toggleFlagged: [];
  move: [];
  delete: [];
  downloadRaw: [];
  downloadPart: [att: AttachmentView];
  openThread: [messageId: string];
}>();

const { t } = useI18n();
const deleteAction = actionOf("delete");
const downloadAction = actionOf("download");
const selectedTab = ref("html");

const images = computed({
  get: () => props.loadImages,
  set: (v: boolean) => emit("update:loadImages", v),
});

const headers = computed(() => headerEntries(props.detail?.headers));

watch(
  () => [props.hasHtml, props.hasText, props.detail?.id] as const,
  () => {
    if (props.hasHtml) selectedTab.value = "html";
    else if (props.hasText) selectedTab.value = "text";
    else selectedTab.value = "headers";
  },
  { immediate: true },
);
</script>

<template>
  <div class="messages-reader position-relative">
    <div class="d-flex align-center flex-wrap ga-2 mb-3">
      <v-btn-inline
        v-if="showBack"
        :icon="mdiArrowLeft"
        @click="emit('back')"
      />
      <div class="text-h6 font-weight-bold flex-grow-1">
        {{ t("messages.reader_title") }}
      </div>
      <template v-if="detail">
        <v-btn-inline
          :prepend-icon="unread ? mdiEmailCheck : mdiEmailMarkAsUnread"
          :loading="busy"
          color="primary"
          @click="emit('toggleSeen')"
        >
          {{ unread ? t("messages.mark_read") : t("messages.mark_unread") }}
        </v-btn-inline>
        <v-btn-inline
          :prepend-icon="flagged ? mdiFlagOff : mdiFlag"
          :loading="busy"
          color="warning"
          @click="emit('toggleFlagged')"
        >
          {{ flagged ? t("messages.unflag") : t("messages.flag") }}
        </v-btn-inline>
        <v-btn-inline
          :prepend-icon="mdiFolderMove"
          :loading="busy"
          color="info"
          @click="emit('move')"
        >
          {{ t("messages.move") }}
        </v-btn-inline>
        <v-btn-inline
          :prepend-icon="mdiDownload"
          :loading="downloading"
          :color="downloadAction.color"
          @click="emit('downloadRaw')"
        >
          {{ t("messages.raw_download") }}
        </v-btn-inline>
        <v-btn-inline
          :prepend-icon="mdiDelete"
          :loading="busy"
          :color="deleteAction.color"
          @click="emit('delete')"
        >
          {{ t("messages.delete") }}
        </v-btn-inline>
      </template>
    </div>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="8"
      shape="form"
    />

    <v-alert
      v-else-if="loadError"
      type="error"
    >
      {{ loadError }}
    </v-alert>

    <div
      v-else-if="detail"
      class="position-relative"
    >
      <AppSectionLoader :status="isRefreshing" />

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("messages.subject") }}
      </div>
      <div class="text-body-large font-weight-medium mb-3">
        {{ detail.subject || t("messages.no_subject") }}
      </div>

      <div class="text-body-medium mb-1">
        <span class="text-medium-emphasis">{{ t("messages.from") }}:</span>
        {{ formatAddresses(detail.from) }}
      </div>
      <div class="text-body-medium mb-1">
        <span class="text-medium-emphasis">{{ t("messages.to") }}:</span>
        {{ formatAddresses(detail.to) }}
      </div>
      <div
        v-if="detail.cc?.length"
        class="text-body-medium mb-1"
      >
        <span class="text-medium-emphasis">{{ t("messages.cc") }}:</span>
        {{ formatAddresses(detail.cc) }}
      </div>
      <div class="text-body-medium mb-4">
        <span class="text-medium-emphasis">{{ t("messages.date") }}:</span>
        {{ formatDateTime(detail.internalDate) }}
        · {{ formatBytes(detail.size) }}
      </div>

      <v-alert
        v-if="rawMissing"
        class="mb-3"
        type="info"
      >
        {{ t("messages.raw_missing") }}
      </v-alert>

      <v-tabs
        v-model="selectedTab"
        class="mb-3"
        color="primary"
      >
        <v-tab
          v-if="hasHtml"
          value="html"
        >
          {{ t("messages.tab_html") }}
        </v-tab>
        <v-tab
          v-if="hasText"
          value="text"
        >
          {{ t("messages.tab_text") }}
        </v-tab>
        <v-tab value="headers">
          {{ t("messages.tab_headers") }}
        </v-tab>
        <v-tab value="attachments">
          {{ t("messages.tab_attachments") }}
        </v-tab>
        <v-tab value="thread">
          {{ t("messages.tab_thread") }}
        </v-tab>
      </v-tabs>

      <div v-if="selectedTab === 'html' && hasHtml">
        <v-switch
          v-model="images"
          :label="t('messages.load_images')"
          class="no-validation mb-2"
          color="primary"
          hide-details
        />
        <SandboxedHtml
          :html="htmlBody"
          :load-images="loadImages"
        />
      </div>

      <pre
        v-else-if="selectedTab === 'text' && hasText"
        class="messages-reader__text pa-3 rounded-lg text-body-small"
      >{{ textBody }}</pre>

      <dl
        v-else-if="selectedTab === 'headers'"
        class="messages-reader__headers"
      >
        <div
          v-for="h in headers"
          :key="h.name"
          class="d-flex flex-wrap ga-2 py-2 border-b"
        >
          <dt class="text-medium-emphasis text-no-wrap text-body-small font-weight-medium pe-2">
            {{ h.name }}
          </dt>
          <dd class="text-break text-body-small mb-0 flex-grow-1">
            {{ h.value }}
          </dd>
        </div>
      </dl>

      <div v-else-if="selectedTab === 'attachments'">
        <div
          v-if="!attachments.length"
          class="text-body-medium text-medium-emphasis"
        >
          {{ t("messages.att_empty") }}
        </div>
        <v-alert
          v-if="attBlocked"
          class="mb-3"
          type="warning"
        >
          {{ t("messages.att_infected") }}
        </v-alert>
        <v-list
          v-if="attachments.length"
          class="bg-transparent"
        >
          <v-list-item
            v-for="att in attachments"
            :key="att.id"
            :title="att.filename || att.id"
            :subtitle="`${att.contentType} · ${formatBytes(att.size)}${att.scan ? ` · ${att.scan}` : ''}`"
          >
            <template #append>
              <v-btn
                v-if="att.scan === 'infected'"
                color="warning"
                @click="emit('downloadPart', att)"
              >
                {{ t("messages.att_blocked") }}
              </v-btn>
              <v-btn
                v-else
                :prepend-icon="mdiDownload"
                :loading="downloading"
                color="secondary"
                @click="emit('downloadPart', att)"
              >
                {{ t("messages.att_download") }}
              </v-btn>
            </template>
          </v-list-item>
        </v-list>
      </div>

      <div v-else-if="selectedTab === 'thread'">
        <div
          v-if="!threadPeers.length"
          class="text-body-medium text-medium-emphasis"
        >
          {{ t("messages.thread_empty") }}
        </div>
        <v-list
          v-else
          class="bg-transparent"
        >
          <v-list-item
            v-for="m in threadPeers"
            :key="m.id"
            :title="m.messageIdHeader || m.emailId"
            :subtitle="formatDateTime(m.internalDate)"
            @click="emit('openThread', m.id)"
          >
            <template #append>
              <v-btn
                color="primary"
                @click.stop="emit('openThread', m.id)"
              >
                {{ t("messages.thread_open") }}
              </v-btn>
            </template>
          </v-list-item>
        </v-list>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.messages-reader__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface));
  max-height: 480px;
  overflow: auto;
}
</style>
