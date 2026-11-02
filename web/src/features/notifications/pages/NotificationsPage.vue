<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useI18n } from "vue-i18n";
import { useDisplay } from "vuetify";
import { mdiAlertCircle, mdiBellOutline, mdiCheck, mdiCheckAll, mdiEmailOpenOutline, mdiFilterVariant, mdiShieldCheckOutline } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader, ServerTableRow } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { useProblem } from "@/composables/useProblem";
import DateUtil from "@/helpers/Date";
import { useNotifyStore } from "@/stores/notify";
import {
  NOTIFICATION_CATEGORIES,
  NOTIFICATION_SEVERITIES,
  type Notification,
} from "@/api/notifications";
import NotificationDetailDrawer from "@/features/notifications/components/NotificationDetailDrawer.vue";
import NotificationRow from "@/features/notifications/components/NotificationRow.vue";
import { useNotificationsList } from "@/features/notifications/composables/useNotificationsList";
import { categorySpec, detailOf, severitySpec, titleOf } from "@/features/notifications/helpers/catalog";
import { useNotificationFeed } from "@/features/notifications/stores/feed";

const { t, te, locale } = useI18n();
const { mdAndUp } = useDisplay();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const feed = useNotificationFeed();
const retryAction = actionOf("retry");

const {
  items,
  total,
  page,
  ipp,
  sortBy,
  filterState,
  filterCategory,
  filterSeverity,
  filterUnread,
  selected,
  selectedUnread,
  selectedResolvable,
  allOnPageSelected,
  someOnPageSelected,
  bulkBusy,
  activeFilterCount,
  empty,
  noResults,
  loadError,
  isFirstLoad,
  isRefreshing,
  load,
  onFilterChange,
  clearFilters,
  toggle,
  toggleAllOnPage,
  bulkMarkRead,
  bulkResolve,
} = useNotificationsList();

const filtersOpen = ref(false);
const detailOpen = ref(false);
const detailItem = ref<Notification | null>(null);
const markingAll = ref(false);

onMounted(() => {
  void load();
});

const enumLabel = (group: string, value: string): string => {
  const key = `notifications.${group}.${value}`;
  return te(key) ? t(key) : value;
};

const headers = computed<ServerTableHeader[]>(() => [
  { key: "select", title: t("notifications.col.select"), sortable: false, forced: true },
  { key: "severity", title: t("notifications.col.severity"), sortable: false },
  { key: "title", title: t("notifications.col.problem"), sortable: false },
  { key: "category", title: t("notifications.col.category"), sortable: false },
  { key: "occurrences", title: t("notifications.col.occurrences"), sortable: false, align: "end" },
  { key: "lastSeen", title: t("notifications.col.last_seen"), sortable: true },
  { key: "firstSeen", title: t("notifications.col.first_seen"), sortable: true },
]);

const stateItems = computed(() => [
  { title: t("notifications.state.open"), value: "open" },
  { title: t("notifications.state.resolved"), value: "resolved" },
]);
const categoryItems = computed(() =>
  NOTIFICATION_CATEGORIES.map((c) => ({ title: enumLabel("category", c), value: c })),
);
const severityItems = computed(() =>
  NOTIFICATION_SEVERITIES.map((s) => ({ title: enumLabel("severity", s), value: s })),
);

// TableCard rows are plain records; the slots hand them back as Notification.
const tableItems = computed(() => items.value as unknown as Record<string, unknown>[]);
const asN = (item: unknown): Notification => item as Notification;

const relative = (iso: string | undefined): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const isUnread = (n: Notification): boolean => n.unread !== false && n.state === "open";

const openDetail = (n: Notification): void => {
  detailItem.value = n;
  detailOpen.value = true;
  if (!isUnread(n)) return;
  feed
    .markRead([n.id])
    .then(() => onChanged({ ...n, unread: false }))
    .catch((err: unknown) => notify.push({ message: messageOf(err), color: "error" }));
};

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  openDetail(asN(row.item));
};

const onChanged = (n: Notification): void => {
  items.value = items.value.map((x) => (x.id === n.id ? n : x));
  if (detailItem.value?.id === n.id) detailItem.value = n;
};

const markAllRead = async (): Promise<void> => {
  if (markingAll.value) return;
  markingAll.value = true;
  try {
    await feed.markAllRead();
    items.value = items.value.map((n) => ({ ...n, unread: false }));
  } catch (err) {
    notify.push({ message: messageOf(err), color: "error" });
  } finally {
    markingAll.value = false;
  }
};
</script>

<template>
  <div>
    <div class="mb-6 d-flex flex-wrap align-center justify-space-between ga-3">
      <div class="d-flex align-center ga-3 min-w-0">
        <v-avatar color="icon-rose">
          <v-icon :icon="mdiBellOutline" />
        </v-avatar>
        <div class="min-w-0">
          <h1 class="text-h5 font-weight-bold mb-1">
            {{ t("notifications.title") }}
          </h1>
          <p class="text-body-medium text-medium-emphasis mb-0">
            {{ t("notifications.subtitle") }}
          </p>
        </div>
      </div>
      <v-btn
        :disabled="feed.summary.unread === 0"
        :loading="markingAll"
        :prepend-icon="mdiCheckAll"
        color="primary"
        data-testid="notifications-mark-all"
        @click="markAllRead"
      >
        {{ t("notifications.mark_all_read") }}
      </v-btn>
    </div>

    <div
      v-if="!mdAndUp"
      class="mb-4"
    >
      <v-btn
        :prepend-icon="mdiFilterVariant"
        color="primary"
        block
        @click="filtersOpen = true"
      >
        {{ t("notifications.filters") }}
        <v-badge
          v-if="activeFilterCount"
          :content="activeFilterCount"
          color="primary"
          class="ms-2"
          inline
        />
      </v-btn>
      <v-bottom-sheet v-model="filtersOpen">
        <AppDrawerPanel
          v-model="filtersOpen"
          :title="t('notifications.filters')"
          as="sheet"
        >
          <div class="d-flex flex-column ga-3">
            <v-select
              v-model="filterState"
              :items="stateItems"
              :label="t('notifications.filter.state')"
              class="no-validation"
              hide-details
              @update:model-value="onFilterChange"
            />
            <AutocompleteSearch
              v-model="filterCategory"
              :items="categoryItems"
              :label="t('notifications.filter.category')"
              class="no-validation"
              clearable
              hide-details
              @update:model-value="onFilterChange"
            />
            <v-select
              v-model="filterSeverity"
              :items="severityItems"
              :label="t('notifications.filter.severity')"
              class="no-validation"
              clearable
              hide-details
              @update:model-value="onFilterChange"
            />
            <v-switch
              v-model="filterUnread"
              :label="t('notifications.filter.unread')"
              class="no-validation"
              hide-details
              @update:model-value="onFilterChange"
            />
          </div>

          <template #footer>
            <v-btn
              v-if="activeFilterCount"
              @click="clearFilters"
            >
              {{ t("notifications.clear_filters") }}
            </v-btn>
            <v-btn
              color="primary"
              @click="filtersOpen = false"
            >
              {{ t("common.close") }}
            </v-btn>
          </template>
        </AppDrawerPanel>
      </v-bottom-sheet>
    </div>

    <v-row
      v-else
      class="mb-4"
      align="center"
    >
      <v-col
        cols="12"
        md="3"
      >
        <v-select
          v-model="filterState"
          :items="stateItems"
          :label="t('notifications.filter.state')"
          class="no-validation"
          hide-details
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        cols="12"
        md="3"
      >
        <AutocompleteSearch
          v-model="filterCategory"
          :items="categoryItems"
          :label="t('notifications.filter.category')"
          class="no-validation"
          clearable
          hide-details
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        cols="12"
        md="3"
      >
        <v-select
          v-model="filterSeverity"
          :items="severityItems"
          :label="t('notifications.filter.severity')"
          class="no-validation"
          clearable
          hide-details
          @update:model-value="onFilterChange"
        />
      </v-col>
      <v-col
        cols="12"
        md="3"
      >
        <v-switch
          v-model="filterUnread"
          :label="t('notifications.filter.unread')"
          class="no-validation"
          hide-details
          @update:model-value="onFilterChange"
        />
      </v-col>
    </v-row>

    <AppCard
      v-if="loadError"
      class="mb-4"
    >
      <div class="d-flex flex-wrap align-center ga-4 pa-2">
        <v-avatar-lead color="error">
          <v-icon :icon="mdiAlertCircle" />
        </v-avatar-lead>
        <div class="flex-grow-1 text-body-large">
          {{ loadError }}
        </div>
        <v-btn
          :color="retryAction.color"
          :prepend-icon="retryAction.icon"
          @click="load"
        >
          {{ t("notifications.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <AppCard
      v-else-if="empty"
      class="mb-4"
    >
      <div
        class="d-flex flex-column align-center text-center pa-6 ga-3"
        data-testid="notifications-empty"
      >
        <v-avatar-hero color="success">
          <v-icon :icon="mdiShieldCheckOutline" />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("notifications.empty_title") }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("notifications.empty_body") }}
        </p>
      </div>
    </AppCard>

    <TableCard
      v-else
      v-model:page="page"
      v-model:ipp="ipp"
      v-model:sort-by="sortBy"
      :headers="headers"
      :items="tableItems"
      :total="total"
      :loading="isFirstLoad || isRefreshing"
      :searchable="false"
      table-name="notifications"
      item-value="id"
      @click:row="onRowClick"
    >
      <template #toolbar>
        <div
          v-if="selected.length"
          class="d-flex flex-wrap align-center ga-2"
          data-testid="notifications-bulk"
        >
          <span class="text-body-medium font-weight-medium me-1">
            {{ t("notifications.selected", { count: selected.length }) }}
          </span>
          <v-btn-inline
            :disabled="!selectedUnread.length || bulkBusy !== null"
            :loading="bulkBusy === 'read'"
            :prepend-icon="mdiEmailOpenOutline"
            color="secondary"
            @click="bulkMarkRead"
          >
            {{ t("notifications.mark_read") }}
          </v-btn-inline>
          <!-- Tooltip on a wrapper: a disabled button gets no pointer events, so it could never explain why. -->
          <span
            v-tippy="t('notifications.bulk_resolve_hint')"
            class="d-inline-flex"
          >
            <v-btn-inline
              :disabled="!selectedResolvable.length || bulkBusy !== null"
              :loading="bulkBusy === 'resolve'"
              :prepend-icon="mdiCheck"
              color="success"
              @click="bulkResolve"
            >
              {{ t("notifications.resolve_info", { count: selectedResolvable.length }) }}
            </v-btn-inline>
          </span>
          <v-btn-inline @click="toggleAllOnPage(false)">
            {{ t("notifications.clear_selection") }}
          </v-btn-inline>
        </div>
      </template>

      <template #no-data>
        <div class="text-center pa-4">
          <div class="text-body-medium mb-2">
            {{ noResults ? t("notifications.no_results") : t("tables.no_results") }}
          </div>
          <v-btn
            v-if="noResults"
            color="primary"
            @click="clearFilters"
          >
            {{ t("notifications.clear_filters") }}
          </v-btn>
        </div>
      </template>

      <template #[`header.select`]>
        <v-checkbox-btn
          :model-value="allOnPageSelected"
          :indeterminate="someOnPageSelected"
          :aria-label="t('notifications.select_page')"
          class="no-validation"
          @update:model-value="toggleAllOnPage(Boolean($event))"
        />
      </template>

      <template #[`item.select`]="{ item }">
        <v-checkbox-btn
          :model-value="selected.includes(asN(item).id)"
          :aria-label="t('notifications.select_row', { title: titleOf(asN(item), t, te) })"
          class="no-validation"
          @click.stop
          @update:model-value="toggle(asN(item).id, Boolean($event))"
        />
      </template>

      <template #[`item.severity`]="{ item }">
        <v-chip
          :color="severitySpec(asN(item).severity).color"
          :prepend-icon="severitySpec(asN(item).severity).icon"
          label
        >
          {{ enumLabel("severity", asN(item).severity) }}
        </v-chip>
      </template>

      <template #[`item.title`]="{ item }">
        <div class="d-flex align-start ga-2 py-2 notifications-title">
          <span
            v-if="isUnread(asN(item))"
            :aria-label="t('notifications.unread')"
            class="notifications-dot bg-primary rounded-circle flex-shrink-0 mt-2"
            role="img"
          />
          <div class="min-w-0">
            <div :class="isUnread(asN(item)) ? 'font-weight-bold' : 'font-weight-medium'">
              {{ titleOf(asN(item), t, te) }}
            </div>
            <div class="text-body-small text-medium-emphasis truncate-1">
              {{ detailOf(asN(item), t, te) }}
            </div>
          </div>
        </div>
      </template>

      <template #[`item.category`]="{ item }">
        <span class="d-inline-flex align-center ga-2 text-no-wrap">
          <v-icon-inline
            :icon="categorySpec(asN(item).category).icon"
            :color="categorySpec(asN(item).category).color"
          />
          {{ enumLabel("category", asN(item).category) }}
        </span>
      </template>

      <template #[`item.occurrences`]="{ item }">
        <span class="font-weight-medium">
          {{ t("notifications.occurrences", { count: asN(item).occurrences }) }}
        </span>
      </template>

      <template #[`item.lastSeen`]="{ item }">
        <span
          v-tippy="DateUtil.formatDateTime(asN(item).lastSeen)"
          class="text-no-wrap"
        >
          {{ relative(asN(item).lastSeen) }}
        </span>
      </template>

      <template #[`item.firstSeen`]="{ item }">
        <span class="text-no-wrap">
          {{ DateUtil.formatDateTime(asN(item).firstSeen) }}
        </span>
      </template>

      <template #card="{ item }">
        <AppCard content-class="pa-1">
          <div class="d-flex align-start">
            <v-checkbox-btn
              :model-value="selected.includes(asN(item).id)"
              :aria-label="t('notifications.select_row', { title: titleOf(asN(item), t, te) })"
              class="no-validation flex-grow-0 mt-3"
              @update:model-value="toggle(asN(item).id, Boolean($event))"
            />
            <v-list class="bg-transparent py-0 flex-grow-1 min-w-0">
              <NotificationRow
                :item="asN(item)"
                class="ps-1"
                @select="openDetail"
              />
            </v-list>
          </div>
        </AppCard>
      </template>
    </TableCard>

    <NotificationDetailDrawer
      v-model="detailOpen"
      :item="detailItem"
      @changed="onChanged"
    />
  </div>
</template>

<style scoped lang="scss">
.notifications-dot {
  width: 8px;
  height: 8px;
}

.notifications-title {
  min-width: 16rem;
  max-width: 36rem;
}
</style>
