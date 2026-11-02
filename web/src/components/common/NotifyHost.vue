<script setup lang="ts">
import { useI18n } from "vue-i18n";
import { mdiClose, mdiContentCopy } from "@mdi/js";
import { useNotifyStore } from "@/stores/notify";

const { t } = useI18n();
const notify = useNotifyStore();

const copyTrace = async (traceId: string): Promise<void> => {
  try {
    await navigator.clipboard.writeText(traceId);
  } catch {
    /* ignore */
  }
};
</script>

<template>
  <div class="notify-host">
    <v-snackbar
      v-for="(item, index) in notify.items"
      :key="item.id"
      :model-value="true"
      :color="item.color === 'grey' ? undefined : item.color"
      :timeout="item.timeout"
      :style="{ bottom: `${16 + index * 72}px` }"
      location="bottom right"
      multi-line
      @update:model-value="(v: boolean) => !v && notify.dismiss(item.id)"
    >
      <div class="d-flex flex-column ga-1">
        <span>{{ item.message }}</span>
        <span
          v-if="item.code"
          class="text-caption"
        >
          {{ item.code }}
        </span>
      </div>

      <template #actions>
        <v-btn
          v-if="item.traceId"
          :icon="mdiContentCopy"
          :aria-label="t('shell.copy_trace')"
          @click="copyTrace(item.traceId)"
        />
        <v-btn
          :icon="mdiClose"
          :aria-label="t('common.close')"
          @click="notify.dismiss(item.id)"
        />
      </template>
    </v-snackbar>
  </div>
</template>
