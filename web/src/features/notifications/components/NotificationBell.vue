<script setup lang="ts">
import { CHROME_ICON_COLOR } from "@/core/config/IconColors";
/**
 * Top-bar bell: unread badge coloured by the highest open severity, live over the `notifications`
 * realtime topic. Desktop opens a menu under the bell; phones a bottom sheet.
 * Self-contained so the layout only mounts `<NotificationBell />`.
 */
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiBell, mdiBellOutline, mdiCheckAll, mdiOpenInNew } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import { useProblem } from "@/composables/useProblem";
import NotificationPanel from "@/features/notifications/components/NotificationPanel.vue";
import { useNotificationBell } from "@/features/notifications/composables/useNotificationBell";

const { t } = useI18n();
const { mobile } = useDisplay();
const { messageOf } = useProblem();

const { feed, open, unread, badge, badgeColor, ariaLabel, markingAll, retryPanel, select, markAllRead, openCentre } =
  useNotificationBell();

const panelError = computed(() => (feed.panelError ? messageOf(feed.panelError) : null));
const hasUnread = computed(() => unread.value > 0);
</script>

<template>
  <div class="d-inline-flex">
    <v-menu
      v-if="!mobile"
      v-model="open"
      :close-on-content-click="false"
      location="bottom end"
      offset="8"
    >
      <template #activator="{ props: menu }">
        <v-btn
          v-tippy="t('notifications.title')"
          :color="CHROME_ICON_COLOR.notifications"
          :aria-label="ariaLabel"
          data-testid="notification-bell"
          icon
          v-bind="menu"
        >
          <v-badge
            :model-value="hasUnread"
            :content="badge"
            :color="badgeColor"
            data-testid="notification-badge"
          >
            <v-icon :icon="hasUnread ? mdiBell : mdiBellOutline" />
          </v-badge>
        </v-btn>
      </template>

      <AppCard
        class="notification-menu"
        content-class="pa-2 notification-menu__body"
        elevated
      >
        <template #header>
          <div class="d-flex align-center ga-2 w-100">
            <span class="text-title-medium flex-grow-1">
              <span class="font-weight-bold">
                {{ t("notifications.title") }}
              </span>
            </span>
            <v-btn-inline
              :disabled="!hasUnread"
              :loading="markingAll"
              :prepend-icon="mdiCheckAll"
              color="primary"
              data-testid="notification-mark-all"
              @click="markAllRead"
            >
              {{ t("notifications.mark_all_read") }}
            </v-btn-inline>
          </div>
        </template>

        <NotificationPanel
          :groups="feed.groups"
          :first-load="feed.panelFirstLoad"
          :refreshing="feed.panelRefreshing"
          :error="panelError"
          @select="select"
          @retry="retryPanel"
        />

        <template #footer>
          <v-btn
            :append-icon="mdiOpenInNew"
            color="primary"
            data-testid="notification-open-centre"
            block
            @click="openCentre"
          >
            {{ t("notifications.open_centre") }}
          </v-btn>
        </template>
      </AppCard>
    </v-menu>

    <template v-else>
      <v-btn
        :color="CHROME_ICON_COLOR.notifications"
        :aria-label="ariaLabel"
        data-testid="notification-bell"
        icon
        @click="open = true"
      >
        <v-badge
          :model-value="hasUnread"
          :content="badge"
          :color="badgeColor"
          data-testid="notification-badge"
        >
          <v-icon :icon="hasUnread ? mdiBell : mdiBellOutline" />
        </v-badge>
      </v-btn>

      <v-bottom-sheet
        v-model="open"
        scrollable
      >
        <AppDrawerPanel
          v-model="open"
          :title="t('notifications.title')"
          content-class="pa-2 notification-sheet__body"
          as="sheet"
        >
          <template #header-actions>
            <v-btn-inline
              :disabled="!hasUnread"
              :loading="markingAll"
              :prepend-icon="mdiCheckAll"
              color="primary"
              data-testid="notification-mark-all"
              @click="markAllRead"
            >
              {{ t("notifications.mark_all_read_short") }}
            </v-btn-inline>
          </template>

          <NotificationPanel
            :groups="feed.groups"
            :first-load="feed.panelFirstLoad"
            :refreshing="feed.panelRefreshing"
            :error="panelError"
            @select="select"
            @retry="retryPanel"
          />

          <template #footer>
            <v-btn
              :append-icon="mdiOpenInNew"
              color="primary"
              data-testid="notification-open-centre"
              block
              @click="openCentre"
            >
              {{ t("notifications.open_centre") }}
            </v-btn>
          </template>
        </AppDrawerPanel>
      </v-bottom-sheet>
    </template>
  </div>
</template>

<style scoped lang="scss">
.notification-menu {
  width: min(420px, calc(100vw - 32px));
}

:deep(.notification-sheet__body) {
  max-height: 70dvh;
  overflow-y: auto;
  overscroll-behavior: contain;
}

:deep(.notification-menu__body) {
  max-height: min(60vh, 560px);
  overflow-y: auto;
  overscroll-behavior: contain;
}
</style>
