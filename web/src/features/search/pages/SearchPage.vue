<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { useRouter } from "vue-router";
import { mdiAlertCircle, mdiBrain, mdiCog, mdiForumOutline, mdiRefresh, mdiTextSearch } from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import DateRangeField from "@/components/DateRangeField.vue";
import TableCard from "@/components/tables/TableCard.vue";
import type { ServerTableHeader, ServerTableRow, ServerTableSortItem } from "@/components/tables/types";
import { actionOf } from "@/constants/Actions";
import { formatDateTime } from "@/helpers/Date";
import { EMPTY_VALUE } from "@/helpers/Format";
import AskPanel from "@/features/search/components/AskPanel.vue";
import SnippetText from "@/features/search/components/SnippetText.vue";
import { useFullTextSearch } from "@/features/search/composables/useFullTextSearch";
import { accountLabelOf } from "@/features/search/helpers/accountCache";
import type { AskCitation, SearchHit } from "@/features/search/helpers/api";

const { t } = useI18n();
const router = useRouter();
const retryAction = actionOf("retry");
const settingsAction = actionOf("settings");

const {
  enabled,
  statusError,
  isCheckingSwitch,
  isLoading,
  q,
  accountId,
  from,
  to,
  page,
  ipp,
  items,
  total,
  totalCapped,
  loadError,
  accountOptions,
  accountsLoading,
  canManageSwitches,
  hasQuery,
  hasFilters,
  init,
  load,
  setQuery,
  onFilterChange,
  clearFilters,
  loadAccounts,
  checkEnabled,
  mode,
  setMode,
  semanticStatus,
  semanticAvailable,
  semanticChecking,
  semanticReasonKey,
} = useFullTextSearch();

/** Results are ordered by relevance: no column sorts. */
const sortBy = computed<ServerTableSortItem[]>({ get: () => [], set: () => undefined });

const isMeaning = computed(() => mode.value === "meaning");

const headers = computed<ServerTableHeader[]>(() => [
  { key: "date", title: t("search.col_date"), sortable: false },
  { key: "account", title: t("search.col_account"), sortable: false },
  { key: "from", title: t("search.col_from"), sortable: false },
  { key: "subject", title: t("search.col_subject"), sortable: false },
  { key: "snippet", title: t("search.col_snippet"), sortable: false },
  ...(isMeaning.value ? [{ key: "score", title: t("search.col_score"), sortable: false }] : []),
]);

const tableItems = computed(() =>
  items.value.map((hit) => ({ ...hit, id: hit.messageId, account: accountLabelOf(hit.accountId) })),
);

const accountItems = computed(() =>
  accountOptions.value.map((a) => ({ title: `${a.name} · ${a.email}`, value: a.id })),
);

/** The toggle's value; Meaning is offered only while semantic search is available for the account. */
const modeModel = computed({
  get: () => mode.value,
  set: (v: "text" | "meaning" | undefined) => setMode(v ?? "text"),
});

/** The line under the toggle: what Meaning needs, or how much of the account is embedded. */
const meaningNote = computed(() => {
  if (!accountId.value) return t("search.meaning_pick_account");
  if (semanticChecking.value && !semanticStatus.value) return "";
  if (!semanticAvailable.value) return t(semanticReasonKey.value);
  const s = semanticStatus.value;
  return s ? t("search.meaning_coverage", { messages: s.messages, indexed: s.indexedMessages }) : "";
});

/** Closeness of a meaning match (cosine similarity) as a percentage. */
const closeness = (score: unknown): string => `${Math.round(Math.max(0, Number(score) || 0) * 100)}%`;

const askOpen = ref(false);
watch(semanticAvailable, (on) => {
  if (!on) askOpen.value = false;
});

const openCitation = (c: AskCitation): void => {
  void router.push({
    name: "messages",
    query: { accountId: c.accountId, mailboxId: c.mailboxId, messageId: c.messageId },
  });
};

const openSettings = (): void => {
  void router.push({ name: "settings" });
};

const onRowClick = (_e: Event, row: ServerTableRow<Record<string, unknown>>): void => {
  const hit = row.item as unknown as SearchHit;
  void router.push({
    name: "messages",
    query: { accountId: hit.accountId, mailboxId: hit.mailboxId, messageId: hit.messageId },
  });
};

onMounted(() => {
  void init();
});
</script>

<template>
  <div>
    <div class="mb-6">
      <h1 class="text-h5 font-weight-bold mb-1">
        {{ t("search.title") }}
      </h1>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("search.subtitle") }}
      </p>
    </div>

    <AppCard
      v-if="statusError"
      class="mb-4"
    >
      <div class="d-flex align-center ga-4 pa-2">
        <v-avatar-lead
          color="error"
        >
          <v-icon
            :icon="mdiAlertCircle"
          />
        </v-avatar-lead>
        <div class="flex-grow-1 text-body-large">
          {{ statusError }}
        </div>
        <v-btn
          :color="retryAction.color"
          :prepend-icon="mdiRefresh"
          @click="checkEnabled"
        >
          {{ t("search.retry") }}
        </v-btn>
      </div>
    </AppCard>

    <AppCard
      v-else-if="enabled === false"
      data-testid="search-disabled"
    >
      <div class="d-flex flex-column align-center text-center pa-6 ga-3">
        <v-avatar-hero
          color="info"
        >
          <v-icon
            :icon="mdiTextSearch"
          />
        </v-avatar-hero>
        <div class="text-h6">
          {{ t("search.disabled_title") }}
        </div>
        <p class="text-body-medium text-medium-emphasis mb-0 max-500">
          {{ t("search.disabled_body") }}
        </p>
        <v-btn
          v-if="canManageSwitches"
          :color="settingsAction.color"
          :prepend-icon="mdiCog"
          data-testid="search-open-settings"
          @click="openSettings"
        >
          {{ t("search.disabled_action") }}
        </v-btn>
        <p
          v-else
          class="text-body-small text-medium-emphasis mb-0"
        >
          {{ t("search.disabled_ask_admin") }}
        </p>
      </div>
    </AppCard>

    <template v-else>
      <div class="d-flex flex-wrap align-center ga-3 mb-4">
        <v-btn-toggle
          v-model="modeModel"
          :aria-label="t('search.mode_label')"
          color="primary"
          data-testid="search-mode"
          mandatory
        >
          <v-btn
            :prepend-icon="mdiTextSearch"
            value="text"
            data-testid="search-mode-text"
          >
            {{ t("search.mode_text") }}
          </v-btn>
          <v-btn
            :prepend-icon="mdiBrain"
            :disabled="!semanticAvailable"
            value="meaning"
            data-testid="search-mode-meaning"
          >
            {{ t("search.mode_meaning") }}
          </v-btn>
        </v-btn-toggle>
        <span
          class="text-body-small text-medium-emphasis flex-grow-1 min-width-0"
          data-testid="search-meaning-note"
        >
          {{ meaningNote }}
        </span>
        <v-btn
          v-if="!askOpen"
          :color="actionOf('open').color"
          :prepend-icon="mdiForumOutline"
          :disabled="!semanticAvailable"
          data-testid="ask-open"
          @click="askOpen = true"
        >
          {{ t("search.ask_open") }}
        </v-btn>
      </div>

      <AskPanel
        v-if="askOpen && semanticAvailable && accountId"
        :account-id="accountId"
        :account-label="accountLabelOf(accountId)"
        :from="from"
        :to="to"
        @close="askOpen = false"
        @open="openCitation"
      />

      <v-alert
        v-if="loadError"

        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="retryAction.color"
            :prepend-icon="mdiRefresh"
            @click="load"
          >
            {{ t("search.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <TableCard
        v-model:page="page"
        v-model:ipp="ipp"
        v-model:sort-by="sortBy"
        :headers="headers"
        :items="tableItems"
        :total="total"
        :loading="isCheckingSwitch || isLoading"
        :search="q"
        table-name="message-search"
        item-value="id"
        @update:search="setQuery"
        @click:row="onRowClick"
      >
        <template #toolbar>
          <v-row
            class="ma-0 ga-3"
            align="center"
            no-gutters
          >
            <v-col
              cols="12"
              md
            >
              <AutocompleteSearch
                v-model="accountId"
                :items="accountItems"
                :loading="accountsLoading"
                :label="t('search.filter_account')"
                class="no-validation"
                server-search
                clearable
                hide-details
                @update:search="loadAccounts"
                @update:model-value="onFilterChange"
              />
            </v-col>
            <v-col
              cols="12"
              md="auto"
            >
              <DateRangeField
                v-model:from="from"
                v-model:to="to"
                :label="t('search.filter_dates')"
                @change="onFilterChange"
              />
            </v-col>
          </v-row>
        </template>

        <template #no-data>
          <div
            class="d-flex flex-column align-center text-center pa-6 ga-2"
            data-testid="search-no-data"
          >
            <v-icon
              :icon="mdiTextSearch"
              color="info"
            />
            <template v-if="isMeaning && (!hasQuery || !accountId)">
              <div class="text-body-large font-weight-medium">
                {{ t("search.meaning_prompt_title") }}
              </div>
              <p class="text-body-small text-medium-emphasis mb-0 max-500">
                {{ t("search.meaning_prompt_body") }}
              </p>
            </template>
            <template v-else-if="!hasQuery">
              <div class="text-body-large font-weight-medium">
                {{ t("search.prompt_title") }}
              </div>
              <p class="text-body-small text-medium-emphasis mb-0 max-500">
                {{ t("search.prompt_body") }}
              </p>
            </template>

            <template v-else>
              <div class="text-body-large font-weight-medium">
                {{ t("search.no_results") }}
              </div>
              <p class="text-body-small text-medium-emphasis mb-0 max-500">
                {{ isMeaning ? t("search.meaning_no_results_body") : t("search.no_results_body") }}
              </p>
              <v-btn
                v-if="hasFilters"
                color="primary"
                @click="clearFilters"
              >
                {{ t("search.clear_filters") }}
              </v-btn>
            </template>
          </div>
        </template>

        <template #[`item.date`]="{ item }">
          <span class="text-no-wrap">
            {{ formatDateTime(String(item.date || "")) }}
          </span>
        </template>

        <template #[`item.account`]="{ item }">
          {{ item.account || EMPTY_VALUE }}
        </template>

        <template #[`item.from`]="{ item }">
          <span class="truncate-1">
            {{ item.from || EMPTY_VALUE }}
          </span>
        </template>

        <template #[`item.subject`]="{ item }">
          <span class="font-weight-medium truncate-2">
            {{ item.subject || t("search.no_subject") }}
          </span>
        </template>

        <template #[`item.snippet`]="{ item }">
          <SnippetText
            :text="String(item.snippet || '')"
            :highlights="(item as unknown as SearchHit).highlights"
            class="truncate-3"
          />
        </template>

        <template #[`item.score`]="{ item }">
          <span class="text-no-wrap font-weight-medium">
            {{ closeness(item.score) }}
          </span>
        </template>
      </TableCard>

      <p
        v-if="totalCapped"
        class="text-body-small text-medium-emphasis mt-2 mb-0"
      >
        {{ t("search.total_capped") }}
      </p>
    </template>
  </div>
</template>
