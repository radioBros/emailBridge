<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiCheck,
  mdiConnection,
  mdiKeyPlus,
  mdiShieldLock,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { ApiProblemError } from "@/api/client";
import { getMcpServer, putMcpServer, type McpServerStatus } from "@/api/mcp";
import { listFeatureSwitches, patchSettings } from "@/api/settings";
import {
  MCP_CLIENTS,
  MCP_CLIENT_FILES,
  dedicatedUrl,
  mainUrl,
  mcpClientConfig,
  portOf,
  type McpClientId,
} from "@/features/ai/helpers/mcp";

const SWITCH = "mcp.enabled";

const { t } = useI18n();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
const saveAction = actionOf("save");
const copyAction = actionOf("copy");

const status = ref<McpServerStatus | null>(null);
const loadError = ref<string | null>(null);
const forbidden = ref(false);
const enabled = ref(false);
const switchLocked = ref(false);
const switchSaving = ref(false);
const port = ref("");
const portSaving = ref(false);
const client = ref<McpClientId>("claudeCode");
const copied = ref<string | null>(null);

const page = typeof window === "undefined"
  ? { origin: "http://localhost", hostname: "localhost" }
  : window.location;

const mainMcpUrl = computed(() =>
  mainUrl(status.value?.mainUrl, status.value?.mainPath ?? "/v1/mcp", page.origin),
);
const dedicatedMcpUrl = computed(() =>
  status.value?.listening && status.value.activeAddr
    ? dedicatedUrl(status.value.activeAddr, status.value.dedicatedPath, page.hostname)
    : "",
);
/** Snippets use the dedicated port when it is up, the main API otherwise. */
const snippetUrl = computed(() => dedicatedMcpUrl.value || mainMcpUrl.value);
const snippet = computed(() => mcpClientConfig(client.value, snippetUrl.value));
const clientItems = computed(() =>
  MCP_CLIENTS.map((value) => ({ title: t(`ai.mcp_client_${value}`), value })),
);
const portDirty = computed(() => port.value.trim() !== portInput(status.value?.listen ?? ""));
const toolCount = computed(() => (status.value?.toolsets ?? []).reduce((n, s) => n + s.tools, 0));

/** ":8090" is shown as "8090"; an address with a host stays as it is. */
const portInput = (listen: string): string => (listen.startsWith(":") ? portOf(listen) : listen);

const load = async (): Promise<void> => {
  start();
  loadError.value = null;
  forbidden.value = false;
  try {
    const [st, sw] = await Promise.all([getMcpServer(), listFeatureSwitches()]);
    status.value = st;
    port.value = portInput(st.listen);
    const entry = (sw.data ?? []).find((s) => s.name === SWITCH);
    enabled.value = entry?.effective === true;
    switchLocked.value = Boolean(entry?.locked);
    finish(true);
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 403) {
      forbidden.value = true;
    } else {
      loadError.value = messageOf(err);
    }
    finish(false);
  }
};

onMounted(() => {
  void load();
});

const toggle = async (value: boolean | null): Promise<void> => {
  const next = value === true;
  switchSaving.value = true;
  try {
    await patchSettings("organization", { [SWITCH]: next });
    enabled.value = next;
    notify.push({ message: next ? t("ai.mcp_enabled_on") : t("ai.mcp_enabled_off"), color: "success" });
  } catch (err) {
    enabled.value = !next;
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    switchSaving.value = false;
  }
};

const savePort = async (): Promise<void> => {
  portSaving.value = true;
  try {
    status.value = await putMcpServer(port.value.trim());
    port.value = portInput(status.value.listen);
    if (status.value.error) {
      notify.push({ message: t("ai.mcp_port_bind_failed", { error: status.value.error }), color: "warning" });
    } else {
      notify.push({ message: t("ai.mcp_port_saved"), color: "success" });
    }
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    portSaving.value = false;
  }
};

const copy = async (id: string, text: string): Promise<void> => {
  try {
    await navigator.clipboard.writeText(text);
    copied.value = id;
    window.setTimeout(() => {
      if (copied.value === id) copied.value = null;
    }, 1500);
  } catch {
    notify.push({ message: t("ai.copy_failed"), color: "error" });
  }
};
</script>

<template>
  <div>
    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="5"
      shape="form"
    />

    <AppCard
      v-else-if="forbidden"
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
        {{ t("ai.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("ai.mcp_no_permission_body") }}
      </p>
    </AppCard>

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
            {{ t("ai.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <AppCard v-if="status">
        <template #header>
          <div class="d-flex align-center ga-3">
            <v-avatar
              color="info"
            >
              <v-icon
                :icon="mdiConnection"
              />
            </v-avatar>
            <div class="min-w-0 flex-grow-1">
              <div class="text-title-medium">
                {{ t("ai.mcp_title") }}
              </div>
              <p class="text-body-small text-medium-emphasis mb-0">
                {{ t("ai.mcp_subtitle") }}
              </p>
            </div>
          </div>
        </template>

        <v-row>
          <v-col cols="12">
            <v-switch
              :model-value="enabled"
              :label="t('ai.mcp_enabled')"
              :hint="switchLocked ? t('ai.mcp_enabled_locked') : t('ai.mcp_enabled_hint')"
              :loading="switchSaving"
              :disabled="switchLocked || switchSaving"
              class="no-validation"
              color="success"
              data-testid="mcp-enabled"
              persistent-hint
              @update:model-value="toggle"
            />
          </v-col>

          <v-col cols="12">
            <v-divider class="mb-4" />
            <div class="text-title-small mb-1">
              {{ t("ai.mcp_urls_title") }}
            </div>
            <p class="text-body-medium text-medium-emphasis mb-3">
              {{ t("ai.mcp_urls_hint") }}
            </p>
            <div class="d-flex flex-column ga-2">
              <div class="d-flex align-center ga-2">
                <v-chip
                  color="primary"
                  label
                >
                  {{ t("ai.mcp_url_main") }}
                </v-chip>
                <code
                  class="text-body-medium text-truncate min-w-0 flex-grow-1"
                  data-testid="mcp-main-url"
                >{{ mainMcpUrl }}</code>
                <v-btn
                  :color="copyAction.color"
                  :icon="copied === 'main' ? mdiCheck : copyAction.icon"
                  :aria-label="t('ai.copy')"
                  @click="copy('main', mainMcpUrl)"
                />
              </div>
              <div
                v-if="dedicatedMcpUrl"
                class="d-flex align-center ga-2"
              >
                <v-chip
                  color="success"
                  label
                >
                  {{ t("ai.mcp_url_dedicated") }}
                </v-chip>
                <code
                  class="text-body-medium text-truncate min-w-0 flex-grow-1"
                  data-testid="mcp-dedicated-url"
                >{{ dedicatedMcpUrl }}</code>
                <v-btn
                  :color="copyAction.color"
                  :icon="copied === 'dedicated' ? mdiCheck : copyAction.icon"
                  :aria-label="t('ai.copy')"
                  @click="copy('dedicated', dedicatedMcpUrl)"
                />
              </div>
            </div>
          </v-col>

          <v-col cols="12">
            <v-divider class="mb-4" />
            <div class="text-title-small mb-1">
              {{ t("ai.mcp_port_title") }}
            </div>
            <div class="d-flex flex-column flex-md-row align-md-start ga-3">
              <v-text-field
                v-model="port"
                :label="t('ai.mcp_port')"
                :hint="status.canChange ? t('ai.mcp_port_hint') : t('ai.mcp_port_owner_only')"
                :disabled="!status.canChange"
                class="no-validation flex-grow-1"
                placeholder="8090"
                inputmode="numeric"
                data-testid="mcp-port"
                persistent-hint
              />
              <v-btn
                v-if="status.canChange"
                :color="saveAction.color"
                :prepend-icon="saveAction.icon"
                :loading="portSaving"
                :disabled="portSaving || !portDirty"
                class="mt-md-2"
                data-testid="mcp-port-save"
                @click="savePort"
              >
                {{ t("ai.mcp_port_save") }}
              </v-btn>
            </div>
            <v-alert
              v-if="status.error"
              type="warning"
              class="mt-3"
              data-testid="mcp-port-error"
            >
              {{ t("ai.mcp_port_bind_failed", { error: status.error }) }}
            </v-alert>
            <p
              v-else-if="status.listening"
              class="text-body-small text-success mt-2 mb-0"
              data-testid="mcp-port-listening"
            >
              {{ t("ai.mcp_port_listening", { addr: status.activeAddr }) }}
            </p>
            <p class="text-body-small text-medium-emphasis mt-2 mb-0">
              {{ t("ai.mcp_port_security") }}
            </p>
          </v-col>

          <v-col cols="12">
            <v-divider class="mb-4" />
            <div class="d-flex flex-column flex-md-row align-md-center ga-3">
              <div class="flex-grow-1 min-w-0">
                <div class="text-title-small mb-1">
                  {{ t("ai.mcp_key_title") }}
                </div>
                <p class="text-body-medium text-medium-emphasis mb-0">
                  {{ t("ai.mcp_key_hint") }}
                </p>
              </div>
              <v-btn
                :color="actionOf('open').color"
                :prepend-icon="mdiKeyPlus"
                :to="{ name: 'api-keys' }"
                data-testid="mcp-api-keys-link"
              >
                {{ t("ai.mcp_key_link") }}
              </v-btn>
            </div>
          </v-col>

          <v-col cols="12">
            <v-divider class="mb-4" />
            <div class="text-title-small mb-1">
              {{ t("ai.mcp_clients_title") }}
            </div>
            <p class="text-body-medium text-medium-emphasis mb-3">
              {{ t("ai.mcp_clients_hint") }}
            </p>
            <v-chip-group
              v-model="client"
              color="primary"
              class="mb-2"
              mandatory
            >
              <v-chip
                v-for="item in clientItems"
                :key="item.value"
                :value="item.value"
                :data-testid="`mcp-client-${item.value}`"
                filter
              >
                {{ item.title }}
              </v-chip>
            </v-chip-group>
            <div class="d-flex align-center ga-2 mb-2">
              <span class="text-body-small text-medium-emphasis flex-grow-1">
                {{ client === "claudeCode" ? t("ai.mcp_client_terminal") : MCP_CLIENT_FILES[client] }}
              </span>
              <v-btn
                :color="copyAction.color"
                :prepend-icon="copied === 'snippet' ? mdiCheck : copyAction.icon"
                data-testid="mcp-snippet-copy"
                @click="copy('snippet', snippet)"
              >
                {{ copied === "snippet" ? t("ai.copied") : t("ai.copy") }}
              </v-btn>
            </div>
            <pre
              class="mcp-snippet text-body-small pa-3 mb-0"
              data-testid="mcp-snippet"
            >{{ snippet }}</pre>
          </v-col>

          <v-col cols="12">
            <v-divider class="mb-4" />
            <div class="text-title-small mb-1">
              {{ t("ai.mcp_toolsets_title", { n: toolCount }) }}
            </div>
            <p class="text-body-medium text-medium-emphasis mb-3">
              {{ t("ai.mcp_toolsets_hint") }}
            </p>
            <div class="d-flex flex-wrap ga-2">
              <v-chip
                v-for="ts in status.toolsets"
                :key="ts.id"
                :data-testid="`mcp-toolset-${ts.id}`"
                label
              >
                {{ ts.id }} · {{ ts.tools }}
              </v-chip>
            </div>
          </v-col>
        </v-row>
      </AppCard>

      <AppSectionLoader :status="isRefreshing" />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.mcp-snippet {
  background: rgba(var(--v-theme-on-surface), 0.05);
  border-radius: 8px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  overflow-x: auto;
  white-space: pre;
}
</style>
