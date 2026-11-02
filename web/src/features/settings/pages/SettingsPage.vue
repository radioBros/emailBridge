<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from "vue";
import { useDisplay } from "vuetify";
import { useI18n } from "vue-i18n";
import {
  mdiArrowDown,
  mdiCog,
  mdiMagnify,
  mdiMagnifyRemoveOutline,
  mdiShieldLock,
} from "@mdi/js";
import AppCard from "@/components/cards/AppCard.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { actionOf } from "@/constants/Actions";
import SettingField from "@/features/settings/components/SettingField.vue";
import SwitchRow from "@/features/settings/components/SwitchRow.vue";
import HighlightText from "@/features/settings/components/HighlightText.vue";
import OidcProvidersPanel from "@/features/settings/components/OidcProvidersPanel.vue";
import AiProviderPanel from "@/features/ai/components/AiProviderPanel.vue";
import McpServerPanel from "@/features/ai/components/McpServerPanel.vue";
import { useSwitchLabels } from "@/composables/useSwitchLabels";
import { useSettings } from "@/features/settings/composables/useSettings";
import { useSettingLabels } from "@/features/settings/composables/useSettingLabels";
import {
  SETTINGS_FIELDS,
  SETTINGS_TABS,
  type SettingFieldDef,
  type SettingsTabId,
} from "@/features/settings/catalog";
import type { FeatureSwitchBody } from "@/api/settings";
import { searchEntries, type SearchEntry } from "@/features/settings/helpers/search";
import { rulesForField } from "@/features/settings/helpers/rules";

const { t, te } = useI18n();
const { switchLabel, switchDescription } = useSwitchLabels();
const { fieldLabel, fieldHint, tabLabel } = useSettingLabels();
const { mdAndUp } = useDisplay();
const saveAction = actionOf("save");
const openAction = actionOf("open");
const tabValid = ref<boolean | null>(true);
const searchValid = ref<boolean | null>(true);
const searchField = ref<{ focus: () => void } | null>(null);

const {
  switches,
  scope,
  tab,
  loadError,
  forbidden,
  saving,
  query,
  searching,
  isFirstLoad,
  isRefreshing,
  canWrite,
  canWriteGlobal,
  fieldsForTab,
  entryOf,
  isLocked,
  isSwitchLocked,
  draftValue,
  draftSwitch,
  setDraft,
  setSwitchDraft,
  isDirty,
  dirtyKeys,
  discard,
  load,
  saveTab,
} = useSettings();

watch(tab, () => {
  tabValid.value = true;
});

watch(searching, () => {
  searchValid.value = true;
  tabValid.value = true;
});

/** Vuetify's clear button sets the model to null; the query stays a string. */
const searchText = computed({
  get: () => query.value,
  set: (v: string | null) => {
    query.value = v ?? "";
  },
});

const FIELD_BY_KEY = new Map(
  Object.values(SETTINGS_FIELDS)
    .flat()
    .map((f) => [f.key, f] as const),
);

/** Save sends every draft, so drafts on tabs that are not on screen are checked with the same rules. */
const draftsValid = computed(() =>
  dirtyKeys.value.every((key) => {
    const field = FIELD_BY_KEY.get(key);
    if (!field) return true;
    return rulesForField(field.kind, key, isLocked(key, field)).every(
      (rule) => rule(draftValue(key)) === true,
    );
  }),
);

const canSave = computed(
  () =>
    isDirty.value &&
    canWrite.value &&
    draftsValid.value &&
    (searching.value ? searchValid.value : tabValid.value) !== false,
);

/* ---------- quick search ---------- */

/** Panels below the tab card that are not plain fields: found by search, opened by scrolling. */
interface SectionDef {
  id: string;
  anchor: string;
  tabId: SettingsTabId;
  titleKey: string;
  subtitleKey: string;
  key: string;
}

type SearchItem =
  | (SearchEntry & { kind: "field"; tabId: SettingsTabId; field: SettingFieldDef })
  | (SearchEntry & { kind: "switch"; tabId: SettingsTabId; sw: FeatureSwitchBody })
  | (SearchEntry & { kind: "section"; tabId: SettingsTabId; anchor: string });

const SECTION_OIDC: SectionDef = {
  id: "oidc",
  anchor: "settings-section-oidc",
  tabId: "security",
  titleKey: "settings.oidc.title",
  subtitleKey: "settings.oidc.subtitle",
  key: "oidc",
};

const SECTION_AI: SectionDef = {
  id: "ai",
  anchor: "settings-section-ai",
  tabId: "switches",
  titleKey: "ai.title",
  subtitleKey: "ai.subtitle",
  key: "ai.provider",
};

const SECTION_MCP: SectionDef = {
  id: "mcp",
  anchor: "settings-section-mcp",
  tabId: "switches",
  titleKey: "ai.mcp_title",
  subtitleKey: "ai.mcp_subtitle",
  key: "mcp.server",
};

const showOidc = computed(() => canWriteGlobal.value && !forbidden.value && !isFirstLoad.value);
const showAi = computed(() => !forbidden.value && !isFirstLoad.value);

const searchIndex = computed((): SearchItem[] => {
  const items: SearchItem[] = [];
  for (const tabId of SETTINGS_TABS) {
    if (tabId === "switches") continue;
    for (const field of SETTINGS_FIELDS[tabId]) {
      items.push({
        kind: "field",
        id: `field:${field.key}`,
        label: fieldLabel(field.key),
        description: fieldHint(field.key),
        key: field.key,
        tab: tabLabel(tabId),
        tabId,
        field,
      });
    }
  }
  for (const sw of switches.value) {
    items.push({
      kind: "switch",
      id: `switch:${sw.name}`,
      label: switchLabel(sw.name),
      description: switchDescription(sw.name, sw.description),
      key: sw.name,
      tab: tabLabel("switches"),
      tabId: "switches",
      sw,
    });
  }
  const sections = [
    showOidc.value ? SECTION_OIDC : null,
    showAi.value ? SECTION_AI : null,
    showAi.value ? SECTION_MCP : null,
  ];
  for (const sec of sections) {
    if (!sec) continue;
    items.push({
      kind: "section",
      id: `section:${sec.id}`,
      label: te(sec.titleKey) ? t(sec.titleKey) : sec.key,
      description: te(sec.subtitleKey) ? t(sec.subtitleKey) : undefined,
      key: sec.key,
      tab: tabLabel(sec.tabId),
      tabId: sec.tabId,
      anchor: sec.anchor,
    });
  }
  return items;
});

const results = computed(() =>
  searching.value ? searchEntries(searchIndex.value, query.value) : [],
);

const resultCountText = computed(() => t("settings.search.count", { n: results.value.length }));

const clearSearch = (): void => {
  query.value = "";
};

const prefersReducedMotion = (): boolean =>
  typeof window !== "undefined" &&
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** "Open" on a panel result: leave search, select its tab, bring the panel into view. */
const openSection = async (tabId: SettingsTabId, anchor: string): Promise<void> => {
  query.value = "";
  tab.value = tabId;
  await nextTick();
  document.getElementById(anchor)?.scrollIntoView?.({
    behavior: prefersReducedMotion() ? "auto" : "smooth",
    block: "start",
  });
};

/** `/` focuses the search unless the user is typing somewhere or a dialog is open. */
const isTypingTarget = (target: EventTarget | null): boolean => {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  if (["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName)) return true;
  return Boolean(target.closest("[role='combobox'], [contenteditable='true'], .v-overlay"));
};

const onGlobalKeydown = (e: KeyboardEvent): void => {
  if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey || e.defaultPrevented) return;
  if (forbidden.value || isFirstLoad.value || isTypingTarget(e.target)) return;
  e.preventDefault();
  searchField.value?.focus();
};

onMounted(() => {
  window.addEventListener("keydown", onGlobalKeydown);
  void load();
});

onBeforeUnmount(() => {
  window.removeEventListener("keydown", onGlobalKeydown);
});

const tabItems = computed(() =>
  SETTINGS_TABS.map((id) => ({
    value: id,
    title: t(`settings.tabs.${id}`),
  })),
);

const scopeItems = computed(() => {
  const items: Array<{ title: string; value: "organization" | "global" }> = [
    { title: t("settings.scope_organization"), value: "organization" },
  ];
  if (canWriteGlobal.value) {
    items.push({ title: t("settings.scope_global"), value: "global" });
  }
  return items;
});
</script>

<template>
  <div>
    <div class="d-flex flex-wrap align-start justify-space-between ga-3 mb-6">
      <div>
        <h1 class="text-h5 font-weight-bold mb-1">
          {{ t("settings.title") }}
        </h1>
        <p class="text-body-medium text-medium-emphasis mb-0">
          {{ t("settings.subtitle") }}
        </p>
      </div>

      <v-btn-toggle
        v-if="!forbidden && canWriteGlobal"
        v-model="scope"
        color="primary"
        class="no-validation"
        divided
        mandatory
      >
        <v-btn
          v-for="item in scopeItems"
          :key="item.value"
          :value="item.value"
        >
          {{ item.title }}
        </v-btn>
      </v-btn-toggle>
    </div>

    <v-text-field
      v-if="!forbidden"
      ref="searchField"
      v-model="searchText"
      :label="t('settings.search.label')"
      :placeholder="t('settings.search.placeholder')"
      :prepend-inner-icon="mdiMagnify"
      :disabled="isFirstLoad"
      type="search"
      autocomplete="off"
      class="no-validation mb-4"
      data-testid="settings-search"
      clearable
      hide-details
      @keydown.esc="clearSearch"
    >
      <template #append-inner>
        <kbd
          v-if="!query"
          v-tippy="t('settings.search.shortcut')"
          class="settings-search-kbd d-none d-md-inline-flex align-center justify-center text-body-small text-medium-emphasis"
        >
          /
        </kbd>
      </template>
    </v-text-field>

    <LoadingSkeleton
      v-if="isFirstLoad"
      :rows="8"
      shape="form"
    />

    <AppCard
      v-else-if="forbidden"
      class="text-center py-8"
    >
      <v-avatar-hero
        color="warning"
        class="mb-4"
      >
        <v-icon
          :icon="mdiShieldLock"
        />
      </v-avatar-hero>
      <div class="text-title-medium mb-1">
        {{ t("settings.no_permission_title") }}
      </div>
      <p class="text-body-medium text-medium-emphasis mb-0">
        {{ t("settings.no_permission_body") }}
      </p>
    </AppCard>

    <div
      v-else
      class="position-relative"
    >
      <v-alert
        v-if="loadError"
        type="error"
        class="mb-4"
      >
        {{ loadError }}
        <template #append>
          <v-btn
            :color="actionOf('retry').color"
            @click="load"
          >
            {{ t("settings.retry") }}
          </v-btn>
        </template>
      </v-alert>

      <AppCard>
        <template #header>
          <div class="d-flex align-center flex-wrap ga-3">
            <v-avatar
              color="primary"
            >
              <v-icon
                :icon="searching ? mdiMagnify : mdiCog"
              />
            </v-avatar>
            <span class="text-title-medium">
              {{ searching ? t("settings.search.results_title") : t(`settings.tabs.${tab}`) }}
            </span>
            <span
              v-if="searching"
              class="text-body-small text-medium-emphasis"
              data-testid="settings-search-count"
            >
              {{ resultCountText }}
            </span>
            <v-chip
              v-if="isDirty"
              color="warning"
              label
            >
              {{ t("settings.dirty") }}
            </v-chip>
          </div>
        </template>

        <!-- Screen readers hear the result count as the query changes. -->
        <div
          class="d-sr-only"
          aria-live="polite"
        >
          {{ searching ? resultCountText : "" }}
        </div>

        <v-form
          v-if="searching"
          v-model="searchValid"
          data-testid="settings-search-results"
        >
          <div
            v-if="!results.length"
            class="d-flex flex-column align-center text-center ga-2 py-10"
            data-testid="settings-search-empty"
          >
            <v-avatar
              color="secondary"
              class="mb-2"
            >
              <v-icon
                :icon="mdiMagnifyRemoveOutline"
              />
            </v-avatar>
            <div class="text-title-medium text-break">
              {{ t("settings.search.empty_title", { query: query.trim() }) }}
            </div>
            <p class="text-body-medium text-medium-emphasis mb-2">
              {{ t("settings.search.empty_body") }}
            </p>
            <v-btn
              :color="actionOf('close').color"
              @click="clearSearch"
            >
              {{ t("settings.search.clear") }}
            </v-btn>
          </div>

          <template
            v-for="r in results"
            :key="r.entry.id"
          >
            <div
              v-if="r.entry.kind === 'field'"
              :data-result="r.entry.id"
              class="border-b"
              data-testid="settings-search-result"
            >
              <v-row>
                <v-col
                  cols="12"
                  md="8"
                  lg="6"
                  class="pb-0"
                >
                  <SettingField
                    :field="r.entry.field"
                    :entry="entryOf(r.entry.key)"
                    :model-value="draftValue(r.entry.key)"
                    :locked="isLocked(r.entry.key, r.entry.field)"
                    @update:model-value="setDraft(r.entry.key, $event)"
                  >
                    <template #label="{ label }">
                      <HighlightText
                        :text="label"
                        :ranges="r.ranges.label"
                      />
                    </template>
                    <template #meta>
                      <v-chip
                        :prepend-icon="mdiCog"
                        label
                      >
                        <HighlightText
                          :text="r.entry.tab"
                          :ranges="r.ranges.tab"
                        />
                      </v-chip>
                    </template>
                    <template #subtitle>
                      <code class="text-body-small text-medium-emphasis d-block text-break">
                        <HighlightText
                          :text="r.entry.key"
                          :ranges="r.ranges.key"
                        />
                      </code>
                      <!-- The control draws its hint unhighlighted; repeat it only when it is why the field matched. -->
                      <div
                        v-if="r.entry.description && r.ranges.description.length"
                        class="text-body-small text-medium-emphasis"
                      >
                        <HighlightText
                          :text="r.entry.description"
                          :ranges="r.ranges.description"
                        />
                      </div>
                    </template>
                  </SettingField>
                </v-col>
              </v-row>
            </div>

            <SwitchRow
              v-else-if="r.entry.kind === 'switch'"
              :sw="r.entry.sw"
              :model-value="draftSwitch(r.entry.key)"
              :locked="isSwitchLocked(r.entry.sw)"
              :data-result="r.entry.id"
              data-testid="settings-search-result"
              @update:model-value="setSwitchDraft(r.entry.key, $event)"
            >
              <template #label="{ label }">
                <HighlightText
                  :text="label"
                  :ranges="r.ranges.label"
                />
              </template>
              <template #meta>
                <v-chip
                  :prepend-icon="mdiCog"
                  label
                >
                  <HighlightText
                    :text="r.entry.tab"
                    :ranges="r.ranges.tab"
                  />
                </v-chip>
              </template>
              <template #name="{ text }">
                <HighlightText
                  :text="text"
                  :ranges="r.ranges.key"
                />
              </template>
              <template #description="{ description }">
                <HighlightText
                  :text="description"
                  :ranges="r.ranges.description"
                />
              </template>
            </SwitchRow>

            <div
              v-else
              :data-result="r.entry.id"
              class="d-flex flex-wrap align-center justify-space-between ga-3 py-3 border-b"
              data-testid="settings-search-result"
            >
              <div class="flex-grow-1 min-w-0">
                <div class="d-flex align-center flex-wrap ga-2 mb-1">
                  <span class="text-body-medium font-weight-medium">
                    <HighlightText
                      :text="r.entry.label"
                      :ranges="r.ranges.label"
                    />
                  </span>
                  <v-chip
                    :prepend-icon="mdiCog"
                    label
                  >
                    <HighlightText
                      :text="r.entry.tab"
                      :ranges="r.ranges.tab"
                    />
                  </v-chip>
                </div>
                <div
                  v-if="r.entry.description"
                  class="text-body-small text-medium-emphasis"
                >
                  <HighlightText
                    :text="r.entry.description"
                    :ranges="r.ranges.description"
                  />
                </div>
              </div>
              <v-btn
                :color="openAction.color"
                :append-icon="mdiArrowDown"
                data-testid="settings-search-open"
                @click="openSection(r.entry.tabId, r.entry.anchor)"
              >
                {{ t("settings.search.open") }}
              </v-btn>
            </div>
          </template>
        </v-form>

        <template v-else>
          <!-- Phone: a select stays visible (horizontal tabs were truncating to
               zero-height / off-screen and failing the suite ready check). -->
          <AutocompleteSearch
            v-if="!mdAndUp"
            v-model="tab"
            :items="tabItems"
            :label="t('settings.title')"
            item-title="title"
            item-value="value"
            data-testid="settings-tab-select"
            class="no-validation mb-4"
            hide-details
          />
          <v-tabs
            v-else
            v-model="tab"
            color="primary"
            class="mb-4 settings-tabs"
            show-arrows
          >
            <v-tab
              v-for="item in tabItems"
              :key="item.value"
              :value="item.value"
              :text="item.title"
            />
          </v-tabs>

          <v-window v-model="tab">
            <v-window-item
              v-for="item in tabItems.filter((i) => i.value !== 'switches')"
              :key="item.value"
              :value="item.value"
            >
              <v-form v-model="tabValid">
                <v-row>
                  <v-col
                    v-for="field in fieldsForTab"
                    :key="field.key"
                    cols="12"
                    md="6"
                  >
                    <SettingField
                      :field="field"
                      :entry="entryOf(field.key)"
                      :model-value="draftValue(field.key)"
                      :locked="isLocked(field.key, field)"
                      @update:model-value="setDraft(field.key, $event)"
                    />
                  </v-col>
                </v-row>
              </v-form>
            </v-window-item>

            <v-window-item value="switches">
              <div
                v-if="!switches.length"
                class="text-body-medium text-medium-emphasis py-4"
              >
                {{ t("settings.switches_empty") }}
              </div>
              <SwitchRow
                v-for="sw in switches"
                :key="sw.name"
                :sw="sw"
                :model-value="draftSwitch(sw.name)"
                :locked="isSwitchLocked(sw)"
                @update:model-value="setSwitchDraft(sw.name, $event)"
              />
            </v-window-item>
          </v-window>
        </template>

        <template #footer>
          <div class="d-flex flex-wrap align-center justify-end ga-2 w-100">
            <v-btn
              :color="actionOf('close').color"
              :disabled="!isDirty || saving"
              @click="discard"
            >
              {{ t("settings.discard") }}
            </v-btn>
            <v-btn
              :color="saveAction.color"
              :prepend-icon="saveAction.icon"
              :loading="saving"
              :disabled="!canSave || saving"
              @click="saveTab"
            >
              {{ t("settings.save") }}
            </v-btn>
          </div>
        </template>
      </AppCard>

      <AppSectionLoader :status="isRefreshing" />
    </div>

    <!-- v-show while searching keeps the panels mounted, so their own state survives a search. -->
    <div
      v-if="showOidc"
      v-show="!searching"
      :id="SECTION_OIDC.anchor"
      class="mt-6"
    >
      <OidcProvidersPanel />
    </div>

    <div
      v-if="showAi"
      v-show="!searching"
      :id="SECTION_AI.anchor"
      class="mt-6"
    >
      <AiProviderPanel />
    </div>

    <div
      v-if="showAi"
      v-show="!searching"
      :id="SECTION_MCP.anchor"
      class="mt-6"
    >
      <McpServerPanel />
    </div>
  </div>
</template>

<style lang="scss" scoped>
.settings-tabs {
  :deep(.v-slide-group__content) {
    flex-wrap: nowrap;
  }
}

.settings-search-kbd {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 4px;
  font-family: inherit;
  height: 1.5rem;
  line-height: 1;
  min-width: 1.5rem;
}
</style>
