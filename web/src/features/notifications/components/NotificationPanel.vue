<script setup lang="ts">
/**
 * Body of the bell panel: open problems grouped critical → warning → info, then the recently
 * resolved ones greyed. Skeleton on the first open, overlay while refreshing what is shown.
 */
import { useI18n } from "vue-i18n";
import { mdiAlertCircle, mdiShieldCheckOutline } from "@mdi/js";
import type { Notification } from "@/api/notifications";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import NotificationRow from "@/features/notifications/components/NotificationRow.vue";
import { severitySpec, type PanelGroup } from "@/features/notifications/helpers/catalog";

defineProps<{
  groups: PanelGroup[];
  firstLoad: boolean;
  refreshing: boolean;
  error: string | null;
}>();

const emit = defineEmits<{
  select: [item: Notification];
  retry: [];
}>();

const { t } = useI18n();
const retryAction = actionOf("retry");

const groupColor = (key: PanelGroup["key"]): string | undefined =>
  key === "resolved" ? undefined : severitySpec(key).color;
</script>

<template>
  <div
    class="position-relative"
    data-testid="notification-panel"
  >
    <LoadingSkeleton
      v-if="firstLoad"
      :rows="4"
      shape="list"
    />

    <div
      v-else-if="error && !groups.length"
      class="d-flex flex-column align-center text-center ga-3 pa-6"
    >
      <v-avatar-lead color="error">
        <v-icon :icon="mdiAlertCircle" />
      </v-avatar-lead>
      <div class="text-body-medium">
        {{ error }}
      </div>
      <v-btn
        :color="retryAction.color"
        :prepend-icon="retryAction.icon"
        @click="emit('retry')"
      >
        {{ t("notifications.retry") }}
      </v-btn>
    </div>

    <div
      v-else-if="!groups.length"
      class="d-flex flex-column align-center text-center ga-2 px-6 py-8"
      data-testid="notification-empty"
    >
      <v-avatar-hero
        color="success"
        class="mb-2"
      >
        <v-icon :icon="mdiShieldCheckOutline" />
      </v-avatar-hero>
      <div class="text-title-medium">
        <span class="font-weight-bold">
          {{ t("notifications.empty_title") }}
        </span>
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0 notification-panel__measure">
        {{ t("notifications.empty_body") }}
      </p>
    </div>

    <v-list
      v-else
      class="bg-transparent py-0"
    >
      <template
        v-for="group in groups"
        :key="group.key"
      >
        <v-list-subheader class="text-uppercase font-weight-bold">
          <span class="d-inline-flex align-center ga-2">
            <span
              :class="groupColor(group.key) ? `bg-${groupColor(group.key)}` : 'bg-surface-variant'"
              class="notification-panel__dot rounded-circle d-inline-block"
              aria-hidden="true"
            />
            {{ t(`notifications.group.${group.key}`) }}
            <span class="text-medium-emphasis font-weight-medium">
              {{ group.items.length }}
            </span>
          </span>
        </v-list-subheader>
        <NotificationRow
          v-for="item in group.items"
          :key="item.id"
          :item="item"
          @select="emit('select', $event)"
        />
      </template>
    </v-list>

    <AppSectionLoader :status="refreshing" />
  </div>
</template>

<style scoped lang="scss">
.notification-panel__dot {
  width: 8px;
  height: 8px;
}

.notification-panel__measure {
  max-width: 32ch;
}
</style>
