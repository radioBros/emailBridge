<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiHistory } from "@mdi/js";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import type { RouteVersion } from "@/api/routes";
import { actionOf } from "@/constants/Actions";
import DateUtil from "@/helpers/Date";

const props = defineProps<{
  modelValue: boolean;
  versions: RouteVersion[];
  currentVersion: number;
  loading: boolean;
  restoring: boolean;
  errorMsg: string | null;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  restore: [version: number];
}>();

const { t, locale } = useI18n();
const restoreAction = actionOf("replay");
const selected = ref<RouteVersion | null>(null);

const open = computed({
  get: () => props.modelValue,
  set: (v: boolean) => emit("update:modelValue", v),
});

watch(
  () => props.modelValue,
  (isOpen) => {
    if (isOpen) selected.value = props.versions[0] ?? null;
  },
);

watch(
  () => props.versions,
  (v) => {
    if (!selected.value && v.length) selected.value = v[0];
  },
);

const selectedDiff = computed(() => {
  const cur = props.versions.find((v) => v.version === props.currentVersion);
  const sel = selected.value;
  if (!sel) return null;
  return {
    filterBefore: cur?.filter ?? "",
    filterAfter: sel.filter,
    mapBefore: cur?.map ?? "",
    mapAfter: sel.map ?? "",
  };
});

const formatAt = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toFormat("dd LLL yyyy HH:mm") ?? iso;
</script>

<template>
  <AppDrawerPanel
    v-model="open"
    :title="t('routes.versions_title')"
    :width="420"
    :loading="loading || restoring"
  >
    <template #prepend>
      <v-avatar
        color="accent"
      >
        <v-icon
          :icon="mdiHistory"
        />
      </v-avatar>
    </template>

    <v-alert
      v-if="errorMsg"
      type="error"
      class="mb-3"
    >
      {{ errorMsg }}
    </v-alert>

    <AppSectionLoader :status="loading && versions.length === 0" />

    <v-list
      v-if="versions.length"
      lines="two"
    >
      <v-list-item
        v-for="v in versions"
        :key="v.id"
        :active="selected?.id === v.id"
        :subtitle="formatAt(v.createdAt)"
        :title="t('routes.versions_item', { n: v.version })"
        @click="selected = v"
      >
        <template #append>
          <v-chip
            v-if="v.version === currentVersion"
            color="success"
          >
            {{ t("routes.versions_current") }}
          </v-chip>
        </template>
      </v-list-item>
    </v-list>

    <div
      v-else-if="!loading"
      class="text-medium-emphasis"
    >
      {{ t("routes.versions_empty") }}
    </div>

    <v-divider class="my-2" />

    <div v-if="selectedDiff">
      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("routes.versions_filter") }}
      </div>
      <pre class="text-caption font-mono mb-3 text-wrap">{{ selectedDiff.filterAfter }}</pre>
      <div class="text-caption text-medium-emphasis mb-1">
        {{ t("routes.versions_map") }}
      </div>
      <pre class="text-caption font-mono mb-0 text-wrap">{{ selectedDiff.mapAfter || "—" }}</pre>
    </div>

    <template
      v-if="selected && selected.version !== currentVersion"
      #footer
    >
      <v-btn
        :color="restoreAction.color"
        :prepend-icon="restoreAction.icon"
        :loading="restoring"
        @click="emit('restore', selected.version)"
      >
        {{ t("routes.versions_restore") }}
      </v-btn>
    </template>
  </AppDrawerPanel>
</template>
