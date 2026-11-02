<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useDisplay } from "vuetify";
import { useI18n } from "vue-i18n";
import { useRoute, useRouter } from "vue-router";
import {
  mdiAccountCircle,
  mdiCloudDownload,
  mdiLogout,
  mdiMenu,
  mdiThemeLightDark,
} from "@mdi/js";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import ConfirmDialog from "@/components/ConfirmDialog.vue";
import AppLogo from "@/components/common/AppLogo.vue";
import NotifyHost from "@/components/common/NotifyHost.vue";
import ThemeToggle from "@/components/common/ThemeToggle.vue";
import LanguageMenu from "@/app/components/LanguageMenu.vue";
import NotificationBell from "@/features/notifications/components/NotificationBell.vue";
import { navigation } from "@/app/navigation";
import { CHROME_ICON_COLOR } from "@/core/config/IconColors";
import { ensureLocale } from "@/app/i18n";
import { isLocale } from "@/app/locales";
import { useRealtime } from "@/composables/useRealtime";
import { useThemePreference } from "@/composables/useThemePreference";
import { useBrandTheme } from "@/composables/useBrandTheme";
import { useUpdateAvailable } from "@/composables/useUpdateAvailable";
import { useSessionStore } from "@/stores/session";
import { useNotifyStore } from "@/stores/notify";
import { useProblem } from "@/composables/useProblem";
import { getMySettings } from "@/api/me";

const { t } = useI18n();
const { smAndUp, mdAndUp, lgAndUp } = useDisplay();
const route = useRoute();
const router = useRouter();
const session = useSessionStore();
const { status: realtimeStatus } = useRealtime();
const { showIndicator, reloadForUpdate } = useUpdateAvailable();
const { boot, toggleLightDark } = useThemePreference();
const { boot: bootBrand } = useBrandTheme();

onMounted(() => {
  void boot();
  void bootBrand();
  void getMySettings()
    .then((s) => {
      const loc = s.settings?.ui?.locale;
      if (isLocale(loc)) return ensureLocale(loc);
    })
    .catch(() => {
      /* keep bootI18n choice */
    });
});

const drawer = ref(true);
watch(
  mdAndUp,
  (up) => {
    drawer.value = up;
  },
  { immediate: true },
);

const rail = computed(() => mdAndUp.value && !lgAndUp.value);
const permanent = computed(() => mdAndUp.value);

const orgItems = computed(() =>
  (session.me?.memberships ?? []).map((m) => ({
    title: m.organizationName,
    value: m.organizationId,
  })),
);

const currentOrg = computed(() =>
  orgItems.value.find((o) => o.value === session.organizationId) ?? orgItems.value[0] ?? null,
);

const orgInitial = computed(() => (currentOrg.value?.title ?? "?").slice(0, 1).toUpperCase());

const isInstanceOwner = computed(
  () => session.profile?.instanceRole === "owner",
);

const navGroups = computed(() =>
  navigation
    .map((group) => ({
      ...group,
      items: group.items.filter(
        (item) => !item.instanceOwnerOnly || isInstanceOwner.value,
      ),
    }))
    .filter((group) => group.items.length > 0),
);

const realtimeColor = computed(() => {
  if (realtimeStatus.value === "open") return "success";
  if (realtimeStatus.value === "connecting") return "warning";
  return "error";
});

const onOrgChange = (value: unknown): void => {
  if (typeof value === "string") session.setOrganization(value);
};

const notify = useNotifyStore();
const { messageOf } = useProblem();

// A failed sign-out (network, server) used to reject unhandled: the menu item looked dead.
const signOut = async (): Promise<void> => {
  try {
    await session.signOut();
    await router.push("/login");
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  }
};
</script>

<template>
  <v-app>
    <v-navigation-drawer
      v-model="drawer"
      :permanent="permanent"
      :temporary="!permanent"
      :rail="rail"
      :expand-on-hover="rail"
      width="260"
      color="surface"
    >
      <template #prepend>
        <div
          :class="[
            'd-flex',
            'align-center',
            rail ? 'justify-center py-3' : 'ga-3 pa-4',
          ]"
        >
          <AppLogo :size="40" />
          <div
            v-if="!rail"
            class="min-w-0"
          >
            <div class="text-title-small font-weight-bold truncate-1">
              EmailBridge
            </div>
            <div class="text-body-small text-medium-emphasis truncate-1">
              {{ t("shell.admin") }}
            </div>
          </div>
        </div>

        <v-divider />
      </template>

      <template
        v-for="group in navGroups"
        :key="group.titleKey"
      >
        <!-- role=navigation (not list): router list-items render as links; axe
             aria-required-children rejects role=list → role=link children. -->
        <nav :aria-label="t(group.titleKey)">
          <v-list-subheader
            v-if="!rail"
            class="text-uppercase px-4"
          >
            {{ t(group.titleKey) }}
          </v-list-subheader>
          <v-list-item
            v-for="item in group.items"
            :key="item.to"
            :to="item.to"
            :title="t(item.titleKey)"
            :active="route.path === item.to"
            color="primary"
            rounded="lg"
            class="mb-1 mx-2"
          >
            <template #prepend>
              <v-icon
                :icon="item.icon"
                :color="item.color"
              />
            </template>
          </v-list-item>
        </nav>
      </template>
    </v-navigation-drawer>

    <v-app-bar
      color="surface"
      height="64"
      rounded="0"
      class="border-b"
      flat
    >
      <v-btn
        v-if="!mdAndUp"
        :icon="mdiMenu"
        :color="CHROME_ICON_COLOR.menu"
        :aria-label="t('shell.toggle_nav')"
        @click="drawer = !drawer"
      />

      <div class="flex-grow-1 px-2 min-w-0">
        <!-- Phone (< sm): compact initials control so bell/theme/locale/avatar keep room. -->
        <v-menu v-if="orgItems.length && !smAndUp">
          <template #activator="{ props: menu }">
            <v-btn
              v-tippy="{ content: currentOrg?.title || t('shell.organisation'), placement: 'bottom' }"
              :aria-label="t('shell.organisation_switch')"
              class="text-none px-1"
              data-testid="org-picker-compact"
              v-bind="menu"
            >
              <v-avatar color="secondary">
                <span class="text-label-large">
                  {{ orgInitial }}
                </span>
              </v-avatar>
            </v-btn>
          </template>
          <v-list min-width="220">
            <v-list-subheader>
              {{ t("shell.organisation") }}
            </v-list-subheader>
            <v-list-item
              v-for="org in orgItems"
              :key="org.value"
              :title="org.title"
              :active="org.value === session.organizationId"
              color="primary"
              @click="onOrgChange(org.value)"
            />
          </v-list>
        </v-menu>
        <AutocompleteSearch
          v-else-if="orgItems.length"
          :model-value="session.organizationId"
          :items="orgItems"
          :label="t('shell.organisation')"
          class="no-validation org-picker"
          style="max-width: 280px;"
          data-testid="org-picker"
          hide-details
          @update:model-value="onOrgChange"
        />
      </div>

      <span
        v-tippy="t('shell.realtime_' + realtimeStatus)"
        :class="['realtime-dot', 'mx-2', 'rounded-circle', 'd-inline-block', `bg-${realtimeColor}`]"
        :aria-label="t('shell.realtime_' + realtimeStatus)"
        role="img"
        tabindex="0"
      />

      <v-btn
        v-if="showIndicator"
        v-tippy="t('update.indicator_tooltip')"
        :icon="mdiCloudDownload"
        :aria-label="t('update.indicator_tooltip')"
        color="info"
        @click="reloadForUpdate()"
      />

      <NotificationBell />

      <ThemeToggle />

      <LanguageMenu />

      <v-menu>
        <template #activator="{ props: menu }">
          <v-btn
            v-bind="menu"
            class="text-none"
          >
            <v-avatar
              color="primary"
            >
              <span class="text-label-large">
                {{ (session.user?.email ?? "?").slice(0, 1).toUpperCase() }}
              </span>
            </v-avatar>
          </v-btn>
        </template>
        <v-list min-width="200">
          <v-list-item
            :title="t('shell.profile')"
            to="/me"
          >
            <template #prepend>
              <v-icon
                :icon="mdiAccountCircle"
                :color="CHROME_ICON_COLOR.profile"
              />
            </template>
          </v-list-item>
          <v-list-item
            :title="t('shell.theme')"
            @click="toggleLightDark"
          >
            <template #prepend>
              <v-icon
                :icon="mdiThemeLightDark"
                :color="CHROME_ICON_COLOR.theme"
              />
            </template>
          </v-list-item>
          <v-divider />
          <v-list-item
            :title="t('shell.sign_out')"
            @click="signOut"
          >
            <template #prepend>
              <v-icon
                :icon="mdiLogout"
                :color="CHROME_ICON_COLOR.signOut"
              />
            </template>
          </v-list-item>
        </v-list>
      </v-menu>
    </v-app-bar>

    <v-main>
      <v-container
        class="pa-4 pa-md-6"
        fluid
      >
        <router-view />
      </v-container>
    </v-main>

    <ConfirmDialog />
    <NotifyHost />
  </v-app>
</template>

<style scoped lang="scss">
.realtime-dot {
  width: 10px;
  height: 10px;
  flex-shrink: 0;
}

// App-bar org picker: keep the nested 40 px control (deliberate default).
.org-picker {
  :deep(.v-input),
  :deep(.v-field) {
    min-height: 40px;
  }
}

// Drawer scroll: never steal layout width (rail icons stay centred at 60 px).
// Global `_scroll.scss` forces classic 12 px webkit bars on descendants of html —
// override here so rail hides the bar and expanded/phone uses a thin overlay.
:deep(.v-navigation-drawer__content) {
  scrollbar-width: none;
  scrollbar-gutter: auto;

  &::-webkit-scrollbar {
    width: 0;
    height: 0;
  }
}

:deep(.v-navigation-drawer:not(.v-navigation-drawer--rail) .v-navigation-drawer__content),
:deep(.v-navigation-drawer--is-hovering .v-navigation-drawer__content) {
  // Extend past the clip so the thin bar overlays instead of shrinking the list
  // (matches global `.thinscroll` 7 px bar; items stay aligned).
  max-width: none;
  scrollbar-width: thin;
  scrollbar-color: rgb(var(--v-theme-primary) / 0.4) transparent;

  &::-webkit-scrollbar {
    width: 7px;
    height: 7px;
  }

  &::-webkit-scrollbar-track {
    background: transparent;
  }

  &::-webkit-scrollbar-thumb {
    background-color: rgb(var(--v-theme-primary));
    border-radius: 6px;
    border: 1px solid transparent;
  }
}
</style>
