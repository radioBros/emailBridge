<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import type { SandboxView } from "@/api/sandbox";
import type { MimeParseResult } from "@/features/sandbox/helpers/mimeParse";
import SandboxedHtml from "@/features/sandbox/components/SandboxedHtml.vue";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";

const props = defineProps<{
  modelValue: boolean;
  detail: SandboxView | null;
  parsed: MimeParseResult | null;
  rawText: string | null;
  rawMissing: boolean;
  loadError: string | null;
  loadImages: boolean;
  isFirstLoad: boolean;
  isRefreshing: boolean;
  deleting: boolean;
  downloading: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  "update:loadImages": [value: boolean];
  download: [];
  delete: [];
}>();

const { t } = useI18n();
const { mdAndUp, width: viewportWidth } = useDisplay();
const downloadAction = actionOf("download");
const deleteAction = actionOf("delete");
const selectedTab = ref("html");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const images = computed({
  get: () => props.loadImages,
  set: (v: boolean) => emit("update:loadImages", v),
});

/** Full screen on mobile; ≈720 px on desktop so tabs fit. */
const drawerWidth = computed(() => (mdAndUp.value ? 720 : viewportWidth.value));

watch(
  () => [props.parsed, props.rawMissing] as const,
  () => {
    if (props.parsed?.html) selectedTab.value = "html";
    else if (props.parsed?.text) selectedTab.value = "text";
    else if (props.rawMissing) selectedTab.value = "headers";
    else selectedTab.value = "raw";
  },
  { immediate: true },
);
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('sandbox.viewer_title')"
    :width="drawerWidth"
  >
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="6"
      class="pa-4"
      shape="form"
    />

    <div
      v-else-if="loadError"
      class="pa-4"
    >
      <v-alert
        type="error"
      >
        {{ loadError }}
      </v-alert>
    </div>

    <div
      v-else-if="detail"
      class="position-relative pa-4"
    >
      <AppSectionLoader :status="isRefreshing" />

      <div class="text-label-large text-medium-emphasis mb-1">
        {{ t("sandbox.col_subject") }}
      </div>
      <div class="text-body-large mb-2">
        {{ detail.subject || "—" }}
      </div>
      <div class="text-body-small text-medium-emphasis mb-4">
        {{ formatDateTime(detail.capturedAt) }}
        · {{ t("sandbox.policy_capture") }}
      </div>

      <div class="text-body-medium mb-1">
        <span class="text-medium-emphasis">{{ t("sandbox.envelope_from") }}:</span>
        {{ detail.envelope.from }}
      </div>
      <div class="text-body-medium mb-4">
        <span class="text-medium-emphasis">{{ t("sandbox.envelope_to") }}:</span>
        {{ (detail.envelope.to ?? []).join(", ") || "—" }}
      </div>

      <v-alert
        v-if="rawMissing"
        class="mb-4"
        type="info"
      >
        {{ t("sandbox.raw_missing") }}
      </v-alert>

      <v-alert
        v-else-if="parsed?.partial"
        class="mb-4"
        type="warning"
      >
        {{ t("sandbox.raw_partial") }}
      </v-alert>

      <v-tabs
        v-model="selectedTab"
        class="mb-3"
        color="primary"
      >
        <v-tab
          v-if="parsed?.html"
          value="html"
        >
          {{ t("sandbox.tab_html") }}
        </v-tab>
        <v-tab
          v-if="parsed?.text"
          value="text"
        >
          {{ t("sandbox.tab_text") }}
        </v-tab>
        <v-tab value="headers">
          {{ t("sandbox.tab_headers") }}
        </v-tab>
        <v-tab value="attachments">
          {{ t("sandbox.tab_attachments") }}
        </v-tab>
        <v-tab
          v-if="!rawMissing"
          value="raw"
        >
          {{ t("sandbox.tab_raw") }}
        </v-tab>
      </v-tabs>

      <div v-show="selectedTab === 'html' && parsed?.html">
        <v-switch
          v-model="images"
          :label="t('sandbox.load_images')"
          :hint="t('sandbox.load_images_hint')"
          class="no-validation mb-2"
          color="primary"
          hide-details="auto"
          inset
        />
        <SandboxedHtml
          :html="parsed!.html!"
          :load-images="loadImages"
        />
      </div>

      <pre
        v-show="selectedTab === 'text'"
        class="sandbox-pre text-body-small pa-3 rounded-lg"
      >{{ parsed?.text || t("sandbox.no_text") }}</pre>

      <dl
        v-show="selectedTab === 'headers'"
        class="mb-2"
      >
        <div
          v-for="(h, i) in parsed?.headers ?? []"
          :key="`${h.name}-${i}`"
          class="d-flex flex-wrap ga-2 py-2 border-b"
        >
          <dt class="text-medium-emphasis text-no-wrap text-body-small font-weight-medium pe-2">
            {{ h.name }}
          </dt>
          <dd class="text-break text-body-small mb-0 flex-grow-1">
            {{ h.value }}
          </dd>
        </div>
        <div
          v-if="!(parsed?.headers?.length)"
          class="text-body-small text-medium-emphasis py-2"
        >
          —
        </div>
      </dl>

      <div v-show="selectedTab === 'attachments'">
        <v-list
          v-if="parsed?.attachments?.length"
          class="bg-transparent pa-0"
        >
          <v-list-item
            v-for="(a, i) in parsed.attachments"
            :key="`${a.filename}-${i}`"
            :title="a.filename"
            :subtitle="`${a.contentType} · ${a.size}`"
          />
        </v-list>
        <div
          v-else
          class="text-body-small text-medium-emphasis py-2"
        >
          {{ t("sandbox.no_attachments") }}
        </div>
      </div>

      <pre
        v-show="selectedTab === 'raw' && rawText"
        class="sandbox-pre text-body-small pa-3 rounded-lg"
      >{{ rawText }}</pre>

      <div class="d-flex flex-wrap ga-2 mt-4">
        <v-btn
          :color="downloadAction.color"
          :prepend-icon="downloadAction.icon"
          :loading="downloading"
          :disabled="rawMissing"
          @click="emit('download')"
        >
          {{ t("sandbox.download_raw") }}
        </v-btn>
        <v-btn
          :color="deleteAction.color"
          :prepend-icon="deleteAction.icon"
          :loading="deleting"
          @click="emit('delete')"
        >
          {{ t("sandbox.delete") }}
        </v-btn>
      </div>
    </div>
  </AppDrawerPanel>
</template>

<style scoped lang="scss">
.sandbox-pre {
  max-height: 360px;
  overflow: auto;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  background: rgb(var(--v-theme-on-surface), 0.04);
}
</style>
