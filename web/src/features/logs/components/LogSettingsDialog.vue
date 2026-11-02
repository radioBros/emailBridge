<script setup lang="ts">
import { computed } from "vue";
import { useI18n } from "vue-i18n";
import AppDialog from "@/components/AppDialog.vue";
import AutocompleteSearch from "@/components/AutocompleteSearch.vue";
import type { LogSettings } from "@/api/logs";
import { actionOf } from "@/constants/Actions";
import { LOG_CATEGORIES } from "@/features/logs/constants";

const props = defineProps<{
  modelValue: boolean;
  settings: LogSettings | null;
  saving: boolean;
  canWrite: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  "update:settings": [value: LogSettings];
  save: [];
}>();

const { t } = useI18n();
const saveAction = actionOf("save");

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

const local = computed({
  get: () => props.settings,
  set: (v: LogSettings | null) => {
    if (v) emit("update:settings", v);
  },
});

const stdoutItems = [
  { title: t("logs.stdout_off"), value: "off" },
  { title: "JSON", value: "json" },
  { title: "Text", value: "text" },
];

const levelItems = ["trace", "debug", "info", "warn", "error", "fatal"].map((l) => ({
  title: t(`logs.level.${l}`),
  value: l,
}));

const ensureCategory = (name: string) => {
  if (!local.value) return;
  const cats = { ...(local.value.categories ?? {}) };
  if (!cats[name]) cats[name] = { enabled: true, level: "info" };
  local.value = { ...local.value, categories: cats };
};

const catEnabled = (name: string): boolean =>
  local.value?.categories?.[name]?.enabled !== false;

const setCatEnabled = (name: string, enabled: boolean | null): void => {
  ensureCategory(name);
  if (!local.value?.categories) return;
  local.value = {
    ...local.value,
    categories: {
      ...local.value.categories,
      [name]: { ...local.value.categories[name], enabled: Boolean(enabled) },
    },
  };
};

const catLevel = (name: string): string =>
  local.value?.categories?.[name]?.level ?? "info";

const setCatLevel = (name: string, level: string): void => {
  ensureCategory(name);
  if (!local.value?.categories) return;
  local.value = {
    ...local.value,
    categories: {
      ...local.value.categories,
      [name]: {
        ...local.value.categories[name],
        level: level as NonNullable<LogSettings["categories"]>[string]["level"],
      },
    },
  };
};

const catRetention = (name: string): string =>
  local.value?.categories?.[name]?.retention ?? "";

const setCatRetention = (name: string, retention: string): void => {
  ensureCategory(name);
  if (!local.value?.categories) return;
  local.value = {
    ...local.value,
    categories: {
      ...local.value.categories,
      [name]: { ...local.value.categories[name], retention: retention || undefined },
    },
  };
};
</script>

<template>
  <AppDialog
    v-model="open"
    :title="t('logs.settings_title')"
    :max-width="720"
    :loading="saving"
    fullscreen-on-mobile
  >
    <template v-if="local">
      <v-row>
        <v-col
          cols="12"
          md="4"
        >
          <v-select
            :model-value="local.stdout ?? 'off'"
            :items="stdoutItems"
            :label="t('logs.settings_stdout')"
            :disabled="!canWrite"
            class="no-validation"
            hide-details
            @update:model-value="local = { ...local, stdout: $event }"
          />
        </v-col>
        <v-col
          cols="12"
          md="4"
        >
          <v-text-field
            :model-value="local.diskCap ?? ''"
            :label="t('logs.settings_disk_cap')"
            :disabled="!canWrite"
            class="no-validation"
            hide-details
            @update:model-value="local = { ...local, diskCap: String($event || '') }"
          />
        </v-col>
        <v-col
          cols="12"
          md="4"
          class="d-flex align-center"
        >
          <v-switch
            :model-value="Boolean(local.hashAddresses)"
            :label="t('logs.settings_hash')"
            :disabled="!canWrite"
            color="success"
            class="no-validation"
            hide-details
            @update:model-value="local = { ...local, hashAddresses: Boolean($event) }"
          />
        </v-col>
      </v-row>

      <div class="text-label-large text-medium-emphasis mt-6 mb-2">
        {{ t("logs.settings_categories") }}
      </div>

      <div
        v-for="cat in LOG_CATEGORIES"
        :key="cat"
        class="d-flex flex-wrap align-center ga-3 py-2 border-b"
      >
        <div class="flex-2">
          <v-switch
            :model-value="catEnabled(cat)"
            :label="cat"
            :disabled="!canWrite"
            color="success"
            class="no-validation"
            hide-details
            @update:model-value="setCatEnabled(cat, $event)"
          />
        </div>
        <div class="flex-3">
          <AutocompleteSearch
            :model-value="catLevel(cat)"
            :items="levelItems"
            :disabled="!canWrite"
            class="no-validation cat-level w-100"
            hide-details
            @update:model-value="setCatLevel(cat, String($event ?? 'info'))"
          />
        </div>
        <div class="flex-3">
          <v-text-field
            :model-value="catRetention(cat)"
            :label="t('logs.settings_retention')"
            :disabled="!canWrite"
            class="no-validation cat-retention w-100"
            hide-details
            @update:model-value="setCatRetention(cat, String($event || ''))"
          />
        </div>
      </div>
    </template>

    <template #footer>
      <v-btn @click="open = false">
        {{ t("logs.close") }}
      </v-btn>
      <v-btn
        :color="saveAction.color"
        :prepend-icon="saveAction.icon"
        :loading="saving"
        :disabled="!canWrite"
        @click="emit('save')"
      >
        {{ t("logs.save") }}
      </v-btn>
    </template>
  </AppDialog>
</template>

<style lang="scss" scoped>
.cat-retention {
  max-width: 8rem;
}
</style>
