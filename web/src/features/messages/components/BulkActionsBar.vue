<script setup lang="ts">
import { useI18n } from "vue-i18n";
import {
  mdiDelete,
  mdiEmailCheck,
  mdiEmailMarkAsUnread,
  mdiFlag,
  mdiFlagOff,
  mdiFolderMove,
} from "@mdi/js";
import { actionOf } from "@/constants/Actions";

defineProps<{
  count: number;
  busy: boolean;
}>();

const emit = defineEmits<{
  read: [];
  unread: [];
  flag: [];
  unflag: [];
  move: [];
  delete: [];
}>();

const { t } = useI18n();
const deleteAction = actionOf("delete");
</script>

<template>
  <v-sheet
    v-if="count > 0"
    class="d-flex flex-wrap align-center ga-2 pa-3 mb-3 rounded-lg border"
    border
  >
    <span class="text-body-medium font-weight-medium me-2 text-primary">
      {{ t("messages.bulk_selected", { n: count }) }}
    </span>
    <v-btn-inline
      :prepend-icon="mdiEmailCheck"
      :loading="busy"
      color="success"
      @click="emit('read')"
    >
      {{ t("messages.bulk_read") }}
    </v-btn-inline>
    <v-btn-inline
      :prepend-icon="mdiEmailMarkAsUnread"
      :loading="busy"
      color="primary"
      @click="emit('unread')"
    >
      {{ t("messages.bulk_unread") }}
    </v-btn-inline>
    <v-btn-inline
      :prepend-icon="mdiFlag"
      :loading="busy"
      color="warning"
      @click="emit('flag')"
    >
      {{ t("messages.bulk_flag") }}
    </v-btn-inline>
    <v-btn-inline
      :prepend-icon="mdiFlagOff"
      :loading="busy"
      color="secondary"
      @click="emit('unflag')"
    >
      {{ t("messages.bulk_unflag") }}
    </v-btn-inline>
    <v-btn-inline
      :prepend-icon="mdiFolderMove"
      :loading="busy"
      color="info"
      @click="emit('move')"
    >
      {{ t("messages.bulk_move") }}
    </v-btn-inline>
    <v-btn-inline
      :prepend-icon="mdiDelete"
      :loading="busy"
      :color="deleteAction.color"
      @click="emit('delete')"
    >
      {{ t("messages.bulk_delete") }}
    </v-btn-inline>
  </v-sheet>
</template>
