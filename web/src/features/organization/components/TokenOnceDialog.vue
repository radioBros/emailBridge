<script setup lang="ts">
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import { actionOf } from "@/constants/Actions";
import { useNotifyStore } from "@/stores/notify";
import type { ApiKeyCreated } from "@/api/organization";

const open = defineModel<boolean>("open", { default: false });

defineProps<{
  created: ApiKeyCreated | null;
}>();

const { t } = useI18n();
const notify = useNotifyStore();

const copyToken = async (token: string): Promise<void> => {
  try {
    await navigator.clipboard.writeText(token);
    notify.push({ message: t("organization.api_keys.copied"), color: "success" });
  } catch {
    notify.push({ message: t("organization.api_keys.copy_failed"), color: "error" });
  }
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('organization.api_keys.token_title')"
    :max-width="720"
    persistent
  >
    <template v-if="created">
      <v-alert
        type="warning"
        class="mb-4"
      >
        {{ t("organization.api_keys.token_warning") }}
      </v-alert>
      <div class="text-label-large text-medium-emphasis mb-1">
        {{ created.name }}
      </div>
      <code class="d-block text-body-small text-break mb-3 pa-3 rounded bg-surface-light">
        {{ created.token }}
      </code>
      <v-btn
        :color="actionOf('copy').color"
        :prepend-icon="actionOf('copy').icon"
        @click="copyToken(created.token)"
      >
        {{ t("organization.api_keys.copy") }}
      </v-btn>
    </template>

    <template #footer>
      <v-btn
        color="primary"
        @click="open = false"
      >
        {{ t("organization.done") }}
      </v-btn>
    </template>
  </AppDialog>
</template>
