<script setup lang="ts">
import { ref } from "vue";
import { useI18n } from "vue-i18n";
import { mdiMenuDown, mdiSync } from "@mdi/js";
import { actionOf } from "@/constants/Actions";

withDefaults(
  defineProps<{
    loading?: boolean;
    disabled?: boolean;
    /** Labelled tonal button (detail header); default is icon-only for table/card rows. */
    labelled?: boolean;
  }>(),
  { labelled: false },
);

const emit = defineEmits<{
  sync: [mode: "fast" | "full"];
}>();

const { t } = useI18n();
const syncAction = actionOf("sync");
const menuOpen = ref(false);
</script>

<template>
  <v-menu
    v-model="menuOpen"
    location="bottom end"
  >
    <template #activator="{ props: menuProps }">
      <v-btn
        v-if="labelled"
        v-bind="menuProps"
        :loading="loading"
        :disabled="disabled"
        :color="syncAction.color"
        :prepend-icon="mdiSync"
        :append-icon="mdiMenuDown"
      >
        {{ t("accounts.action_sync") }}
      </v-btn>
      <v-btn
        v-else
        v-tippy="t('accounts.action_sync')"
        v-bind="menuProps"
        :loading="loading"
        :disabled="disabled"
        :icon="mdiSync"
        :color="syncAction.color"
        :aria-label="t('accounts.action_sync')"
      />
    </template>
    <v-list>
      <v-list-item
        :title="t('accounts.action_sync')"
        :subtitle="t('accounts.action_sync_fast_hint')"
        @click="emit('sync', 'fast'); menuOpen = false"
      />
      <v-list-item
        :title="t('accounts.action_sync_full')"
        :subtitle="t('accounts.action_sync_full_hint')"
        @click="emit('sync', 'full'); menuOpen = false"
      />
    </v-list>
  </v-menu>
</template>
