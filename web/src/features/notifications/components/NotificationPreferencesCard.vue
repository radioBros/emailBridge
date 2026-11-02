<script setup lang="ts">
/**
 * Profile → Notifications: the email on/off toggle, digest and minimum severity, per-category
 * bell / email choices, and quiet hours. Self-contained (own load state) so the profile page only
 * mounts it. When the organisation switch `notifications.email` is off, email choices are read-only
 * with the reason shown.
 */
import { computed, onMounted } from "vue";
import { useI18n } from "vue-i18n";
import { mdiBellCogOutline, mdiEmailOffOutline } from "@mdi/js";
import { NOTIFICATION_CATEGORIES, NOTIFICATION_SEVERITIES } from "@/api/notifications";
import AppCard from "@/components/cards/AppCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import { categorySpec } from "@/features/notifications/helpers/catalog";
import { isTime, useNotificationPrefs } from "@/features/notifications/composables/useNotificationPrefs";

const { t } = useI18n();
const saveAction = actionOf("save");
const retryAction = actionOf("retry");

const {
  draft,
  loadError,
  saving,
  dirty,
  quietValid,
  emailBlocked,
  roleDefault,
  usingRoleDefault,
  isFirstLoad,
  isRefreshing,
  load,
  save,
  reset,
} = useNotificationPrefs();

onMounted(() => {
  void load();
});

const digestItems = computed(() =>
  (["immediate", "hourly", "daily"] as const).map((v) => ({ title: t(`notifications.prefs.digest_${v}`), value: v })),
);
const severityItems = computed(() =>
  [...NOTIFICATION_SEVERITIES].reverse().map((v) => ({ title: t(`notifications.prefs.min_${v}`), value: v })),
);

const zoneItems = computed(() => {
  let zones: string[] = [];
  try {
    zones = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.("timeZone") ?? [];
  } catch {
    zones = [];
  }
  if (!zones.includes("UTC")) zones = ["UTC", ...zones];
  if (draft.value.quietTz && !zones.includes(draft.value.quietTz)) zones = [draft.value.quietTz, ...zones];
  return zones.map((z) => ({ title: z.replace(/_/g, " "), value: z }));
});

const emailOff = computed(() => emailBlocked.value || !draft.value.emailEnabled);
const showRoleHint = computed(() => usingRoleDefault.value && !roleDefault.value && !emailBlocked.value);
const timeRules = [(v: string) => isTime(v) || t("notifications.prefs.time_invalid")];
</script>

<template>
  <AppCard data-testid="notification-prefs">
    <template #header>
      <div class="d-flex align-center ga-3">
        <v-avatar color="icon-rose">
          <v-icon :icon="mdiBellCogOutline" />
        </v-avatar>
        <span class="text-title-medium font-weight-bold">
          {{ t("notifications.prefs.title") }}
        </span>
        <v-chip
          v-if="dirty"
          color="warning"
          label
        >
          {{ t("notifications.prefs.unsaved") }}
        </v-chip>
      </div>
    </template>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="5"
      shape="form"
    />

    <div
      v-else-if="loadError"
      class="d-flex flex-wrap align-center ga-3"
    >
      <span class="flex-grow-1 text-body-medium">
        {{ loadError }}
      </span>
      <v-btn
        :color="retryAction.color"
        :prepend-icon="retryAction.icon"
        @click="load"
      >
        {{ t("notifications.retry") }}
      </v-btn>
    </div>

    <v-row v-else>
      <v-col
        cols="12"
        lg="6"
        class="d-flex flex-column ga-6"
      >
        <section class="d-flex flex-column ga-3">
          <div
            v-if="emailBlocked"
            class="prefs-note d-flex align-start ga-3 pa-3 rounded-lg"
            data-testid="notification-prefs-blocked"
            role="note"
          >
            <v-icon
              :icon="mdiEmailOffOutline"
              color="info"
            />
            <span class="text-body-medium">
              {{ t("notifications.prefs.org_disabled") }}
            </span>
          </div>

          <div class="d-flex align-start justify-space-between ga-4">
            <div class="min-w-0">
              <div class="text-body-large font-weight-medium">
                {{ t("notifications.prefs.email_label") }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{ showRoleHint ? t("notifications.prefs.email_role_hint") : t("notifications.prefs.email_hint") }}
              </div>
            </div>
            <v-switch
              v-model="draft.emailEnabled"
              :disabled="emailBlocked || saving"
              :aria-label="t('notifications.prefs.email_label')"
              color="success"
              class="no-validation flex-grow-0"
              data-testid="notification-prefs-email"
              hide-details
            />
          </div>

          <v-row>
            <v-col
              cols="12"
              sm="6"
            >
              <v-select
                v-model="draft.digest"
                :items="digestItems"
                :label="t('notifications.prefs.digest')"
                :disabled="emailOff || saving"
                class="no-validation"
                hide-details
              />
            </v-col>
            <v-col
              cols="12"
              sm="6"
            >
              <v-select
                v-model="draft.minSeverity"
                :items="severityItems"
                :label="t('notifications.prefs.min_severity')"
                :disabled="emailOff || saving"
                class="no-validation"
                hide-details
              />
            </v-col>
          </v-row>
        </section>

        <section class="d-flex flex-column ga-3">
          <div class="d-flex align-start justify-space-between ga-4">
            <div class="min-w-0">
              <div class="text-body-large font-weight-medium">
                {{ t("notifications.prefs.quiet") }}
              </div>
              <div class="text-body-small text-medium-emphasis">
                {{ t("notifications.prefs.quiet_hint") }}
              </div>
            </div>
            <v-switch
              v-model="draft.quietEnabled"
              :disabled="emailOff || saving"
              :aria-label="t('notifications.prefs.quiet')"
              color="success"
              class="no-validation flex-grow-0"
              hide-details
            />
          </div>
          <v-row v-if="draft.quietEnabled">
            <v-col
              cols="6"
            >
              <v-text-field
                v-model="draft.quietStart"
                :label="t('notifications.prefs.quiet_start')"
                :rules="timeRules"
                :disabled="emailOff || saving"
                type="time"
              />
            </v-col>
            <v-col
              cols="6"
            >
              <v-text-field
                v-model="draft.quietEnd"
                :label="t('notifications.prefs.quiet_end')"
                :rules="timeRules"
                :disabled="emailOff || saving"
                type="time"
              />
            </v-col>
            <v-col
              cols="12"
            >
              <AutocompleteSearch
                v-model="draft.quietTz"
                :items="zoneItems"
                :label="t('notifications.prefs.quiet_tz')"
                :disabled="emailOff || saving"
                class="no-validation"
                hide-details
              />
            </v-col>
          </v-row>
        </section>
      </v-col>
      <v-col
        cols="12"
        lg="6"
      >
        <section>
          <div class="d-flex align-center ga-2 pb-2 border-b">
            <span class="text-label-large font-weight-bold flex-grow-1">
              {{ t("notifications.prefs.categories") }}
            </span>
            <span class="prefs-col text-label-medium text-medium-emphasis text-center">
              {{ t("notifications.prefs.col_bell") }}
            </span>
            <span class="prefs-col text-label-medium text-medium-emphasis text-center">
              {{ t("notifications.prefs.col_email") }}
            </span>
          </div>
          <div
            v-for="c in NOTIFICATION_CATEGORIES"
            :key="c"
            class="d-flex align-center ga-2 py-1 border-b"
          >
            <span class="d-flex align-center ga-3 flex-grow-1 min-w-0">
              <v-icon-inline
                :icon="categorySpec(c).icon"
                :color="categorySpec(c).color"
              />
              <span class="text-body-medium truncate-1">
                {{ t(`notifications.category.${c}`) }}
              </span>
            </span>
            <span class="prefs-col d-flex justify-center">
              <v-checkbox-btn
                v-model="draft.inApp[c]"
                :aria-label="t('notifications.prefs.bell_for', { category: t(`notifications.category.${c}`) })"
                :disabled="saving"
                class="no-validation flex-grow-0"
              />
            </span>
            <span class="prefs-col d-flex justify-center">
              <v-checkbox-btn
                v-model="draft.email[c]"
                :aria-label="t('notifications.prefs.email_for', { category: t(`notifications.category.${c}`) })"
                :disabled="emailOff || saving"
                class="no-validation flex-grow-0"
              />
            </span>
          </div>
        </section>
      </v-col>
    </v-row>

    <template
      v-if="!isFirstLoad && !loadError"
      #footer
    >
      <div class="d-flex flex-wrap align-center justify-end ga-2 w-100">
        <v-btn
          :disabled="!dirty || saving"
          @click="reset"
        >
          {{ t("notifications.prefs.discard") }}
        </v-btn>
        <v-btn
          :color="saveAction.color"
          :prepend-icon="saveAction.icon"
          :loading="saving"
          :disabled="!dirty || !quietValid || saving"
          data-testid="notification-prefs-save"
          @click="save"
        >
          {{ t("notifications.prefs.save") }}
        </v-btn>
      </div>
    </template>

    <template #root>
      <AppSectionLoader :status="isRefreshing" />
    </template>
  </AppCard>
</template>

<style scoped lang="scss">
.prefs-note {
  background: rgb(var(--v-theme-info), 0.12);
}

.prefs-col {
  flex: 0 0 56px;
}
</style>
