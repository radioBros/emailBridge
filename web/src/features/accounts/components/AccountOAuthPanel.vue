<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import {
  mdiContentCopy,
  mdiKeyVariant,
  mdiLinkOff,
  mdiLinkVariant,
  mdiSignature,
} from "@mdi/js";
import AppDialog from "@/components/AppDialog.vue";
import AppCard from "@/components/cards/AppCard.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useConfirm } from "@/composables/useConfirm";
import { useNotifyStore } from "@/stores/notify";
import DateUtil from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import {
  disconnectAccountOauth,
  exportAccountOauthToken,
  getAccountOauth,
  listAccountServerSignatures,
  startAccountOauth,
  type AccountOAuthView,
  type ServerSignatures,
} from "@/api/accountsOauth";

const props = defineProps<{
  accountId: string;
  provider?: string;
  tokenExportEnabled?: boolean;
  canExportToken?: boolean;
}>();

const emit = defineEmits<{
  changed: [];
}>();

const { t } = useI18n();
const notify = useNotifyStore();
const { messageOf } = useProblem();
const loadState = useLoadState();

const view = ref<AccountOAuthView | null>(null);
const sigs = ref<ServerSignatures | null>(null);
const error = ref<string | null>(null);
const appId = ref("");
const tokenDialog = ref(false);
const exportedToken = ref("");
const busy = ref(false);

const isFirst = computed(() => loadState.isFirstLoad.value);
const refreshing = computed(() => loadState.isRefreshing.value || busy.value);

const refresh = async (): Promise<void> => {
  error.value = null;
  loadState.start();
  try {
    view.value = await getAccountOauth(props.accountId);
    // Server signatures exist only on the Gmail and Microsoft Graph APIs; IMAP accounts get 400.
    sigs.value = null;
    if (props.provider === "gmail" || props.provider === "graph") {
      try {
        sigs.value = await listAccountServerSignatures(props.accountId);
      } catch {
        sigs.value = null;
      }
    }
    loadState.finish(true);
  } catch (e: unknown) {
    error.value = messageOf(e);
    loadState.finish(false);
  }
};

onMounted(() => {
  void refresh();
});

watch(
  () => props.accountId,
  () => {
    loadState.reset();
    void refresh();
  },
);

const formatWhen = (iso?: string): string =>
  iso ? DateUtil.formatDateTime(iso) : EMPTY_VALUE;

const onConnect = async (): Promise<void> => {
  if (!appId.value.trim()) return;
  busy.value = true;
  try {
    const res = await startAccountOauth(props.accountId, appId.value.trim());
    if (res.authorizeUrl) {
      window.location.href = res.authorizeUrl;
      return;
    }
    notify.push({ message: t("accounts.oauth_started"), color: "success" });
    await refresh();
    emit("changed");
  } catch (e: unknown) {
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    busy.value = false;
  }
};

const confirm = useConfirm();

const onDisconnect = async (): Promise<void> => {
  const ok = await confirm({
    title: t("accounts.oauth_disconnect_title"),
    body: t("accounts.oauth_disconnect_body"),
    variant: "danger",
  });
  if (!ok) return;
  busy.value = true;
  try {
    await disconnectAccountOauth(props.accountId);
    notify.push({ message: t("accounts.oauth_disconnected"), color: "success" });
    await refresh();
    emit("changed");
  } catch (e: unknown) {
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    busy.value = false;
  }
};

const onExport = async (): Promise<void> => {
  busy.value = true;
  try {
    const res = await exportAccountOauthToken(props.accountId);
    exportedToken.value = res.accessToken;
    tokenDialog.value = true;
  } catch (e: unknown) {
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    busy.value = false;
  }
};

const copyToken = async (): Promise<void> => {
  try {
    await navigator.clipboard.writeText(exportedToken.value);
    notify.push({ message: t("accounts.oauth_token_copied"), color: "success" });
  } catch {
    notify.push({ message: t("accounts.oauth_token_copy_failed"), color: "error" });
  }
};

const closeToken = (): void => {
  tokenDialog.value = false;
  exportedToken.value = "";
};
</script>

<template>
  <div class="position-relative">
    <LoadingSkeleton
      v-if="isFirst"
      :rows="4"
      shape="form"
    />
    <template v-else>
      <AppSectionLoader :status="refreshing" />
      <v-alert
        v-if="error"
        type="error"
        class="mb-4"
      >
        {{ error }}
      </v-alert>

      <AppCard class="mb-4 h-auto">
        <template #header>
          <span class="text-title-medium font-weight-bold">
            {{ t("accounts.oauth_title") }}
          </span>
        </template>
        <div
          v-if="view?.connected"
          class="d-flex flex-column ga-2"
        >
          <div class="text-body-medium">
            {{ t("accounts.oauth_connected") }}
            <span class="font-weight-medium">{{ view.appId }}</span>
          </div>
          <div
            v-if="view.mode"
            class="text-body-medium text-medium-emphasis"
          >
            {{ t("accounts.oauth_mode") }}: {{ view.mode }}
          </div>
          <div class="text-body-medium text-medium-emphasis">
            {{ t("accounts.oauth_expires") }}: {{ formatWhen(view.expiresAt) }}
          </div>
          <div
            v-if="view.scopes?.length"
            class="text-caption text-medium-emphasis"
          >
            {{ view.scopes.join(" · ") }}
          </div>
          <div class="d-flex flex-wrap ga-2 mt-2">
            <v-btn
              :color="actionOf('delete').color"
              :prepend-icon="mdiLinkOff"
              @click="onDisconnect"
            >
              {{ t("accounts.oauth_disconnect") }}
            </v-btn>
            <v-btn
              v-if="tokenExportEnabled && canExportToken"
              :color="actionOf('download').color"
              :prepend-icon="mdiKeyVariant"
              @click="onExport"
            >
              {{ t("accounts.oauth_export_token") }}
            </v-btn>
          </div>
        </div>
        <div
          v-else
          class="d-flex flex-column ga-3"
        >
          <p class="text-body-medium text-medium-emphasis mb-0">
            {{ t("accounts.oauth_not_connected") }}
          </p>
          <v-text-field
            v-model="appId"
            :label="t('accounts.oauth_app_id')"
            class="no-validation"
            hide-details="auto"
          />
          <div>
            <v-btn
              :color="actionOf('create').color"
              :prepend-icon="mdiLinkVariant"
              :disabled="!appId.trim()"
              @click="onConnect"
            >
              {{ t("accounts.oauth_connect") }}
            </v-btn>
          </div>
        </div>
      </AppCard>

      <AppCard class="h-auto">
        <template #header>
          <span class="text-title-medium font-weight-bold">
            <v-icon-inline
              :icon="mdiSignature"
              class="me-1"
            />
            {{ t("accounts.signatures_title") }}
          </span>
        </template>
        <div
          v-if="!sigs"
          class="text-body-medium text-medium-emphasis"
        >
          {{ t("accounts.signatures_unavailable") }}
        </div>
        <div
          v-else-if="sigs.reason && !(sigs.data?.length)"
          class="text-body-medium text-medium-emphasis"
        >
          {{ t("accounts.signatures_graph_reason") }}
        </div>
        <div
          v-else-if="!(sigs.data?.length)"
          class="text-body-medium text-medium-emphasis"
        >
          {{ t("accounts.signatures_empty") }}
        </div>
        <v-list
          v-else
          class="bg-transparent pa-0"
        >
          <v-list-item
            v-for="(s, i) in sigs.data ?? []"
            :key="i"
            :title="s.displayName || s.email"
            :subtitle="s.email"
          >
            <template #append>
              <v-chip
                v-if="s.isDefault"
                color="primary"
              >
                {{ t("accounts.signatures_default") }}
              </v-chip>
            </template>
          </v-list-item>
        </v-list>
      </AppCard>
    </template>

    <AppDialog
      v-model="tokenDialog"
      :title="t('accounts.oauth_token_dialog_title')"
      :max-width="560"
      @after-leave="closeToken"
    >
      <p class="text-body-medium text-medium-emphasis mb-3">
        {{ t("accounts.oauth_token_dialog_body") }}
      </p>
      <v-textarea
        :model-value="exportedToken"
        class="no-validation font-mono"
        rows="3"
        readonly
        hide-details
      />

      <template #footer>
        <v-btn
          :prepend-icon="mdiContentCopy"
          color="primary"
          @click="copyToken"
        >
          {{ t("accounts.oauth_token_copy") }}
        </v-btn>
        <v-btn @click="closeToken">
          {{ t("accounts.oauth_token_close") }}
        </v-btn>
      </template>
    </AppDialog>
  </div>
</template>
