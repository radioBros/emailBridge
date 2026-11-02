<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { mdiAlert, mdiContentCopy, mdiDownload } from "@mdi/js";
import { actionOf } from "@/constants/Actions";
import { useNotifyStore } from "@/stores/notify";

const props = defineProps<{
  codes: string[];
}>();

const emit = defineEmits<{
  done: [];
}>();

const { t } = useI18n();
const notify = useNotifyStore();
const copyAction = actionOf("copy");
const downloadAction = actionOf("download");

const copyCodes = async (): Promise<void> => {
  await navigator.clipboard.writeText(props.codes.join("\n"));
  notify.push({ message: t("auth.enrol_copy_codes"), color: "success" });
};

const downloadCodes = (): void => {
  const blob = new Blob([props.codes.join("\n") + "\n"], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = "emailbridge-backup-codes.txt";
  a.click();
  URL.revokeObjectURL(url);
};
</script>

<template>
  <div>
    <v-alert
      :icon="mdiAlert"
      type="warning"
      class="mb-4"
    >
      {{ t("auth.enrol_backup_warn") }}
    </v-alert>
    <p class="text-body-medium text-medium-emphasis mb-3">
      {{ t("auth.enrol_backup_body") }}
    </p>
    <div class="auth-backup-grid mb-4">
      <code
        v-for="code in codes"
        :key="code"
        class="auth-backup-code text-body-medium"
      >
        {{ code }}
      </code>
    </div>
    <div class="d-flex flex-wrap ga-2 mb-4">
      <v-btn
        :color="copyAction.color"
        :prepend-icon="mdiContentCopy"
        @click="copyCodes"
      >
        {{ t("auth.enrol_copy_codes") }}
      </v-btn>
      <v-btn
        :color="downloadAction.color"
        :prepend-icon="mdiDownload"
        @click="downloadCodes"
      >
        {{ t("auth.enrol_download_codes") }}
      </v-btn>
    </div>
    <v-btn
      color="success"
      block
      @click="emit('done')"
    >
      {{ t("auth.enrol_finish") }}
    </v-btn>
  </div>
</template>

<style scoped lang="scss">
.auth-backup-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0.5rem;
}

.auth-backup-code {
  display: block;
  padding: 0.5rem 0.75rem;
  border-radius: 8px;
  background: rgb(var(--v-theme-surface-variant) / 35%);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  letter-spacing: 0.04em;
  text-align: center;
}
</style>
