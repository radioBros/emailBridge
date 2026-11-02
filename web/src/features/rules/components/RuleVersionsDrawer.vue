<script setup lang="ts">
/** Rule versions, newest first, each with its diff against the current rule and a restore action. */
import { computed, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiHistory, mdiRestore } from "@mdi/js";
import AppDrawerPanel from "@/components/AppDrawerPanel.vue";
import AppSectionLoader from "@/components/states/AppSectionLoader.vue";
import LoadingSkeleton from "@/components/states/LoadingSkeleton.vue";
import { listRuleVersions, type RuleTargetKind, type RuleVersion } from "@/api/rules";
import { useConfirm } from "@/composables/useConfirm";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import DateUtil from "@/helpers/Date";
import { useNotifyStore } from "@/stores/notify";
import { lineDiff } from "@/features/rules/model/code";

const props = defineProps<{
  modelValue: boolean;
  kind: RuleTargetKind;
  targetId: string;
  currentVersion: number;
  currentFilter: string;
  currentMap: string;
  restoring: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: boolean];
  restore: [version: number];
}>();

const { t, locale } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const confirm = useConfirm();
const load = useLoadState();
const versions = ref<RuleVersion[]>([]);
const open = ref<number | null>(null);

const fetch = async (): Promise<void> => {
  if (!props.targetId) return;
  load.start();
  try {
    const res = await listRuleVersions(props.kind, props.targetId, { limit: 50 });
    versions.value = res.data ?? [];
    load.finish(true);
  } catch (e) {
    load.finish(false);
    notify.push({ message: messageOf(e), color: "error" });
  }
};

watch(
  () => [props.modelValue, props.currentVersion] as const,
  ([isOpen]) => {
    if (isOpen) void fetch();
  },
  { immediate: true },
);

const relative = (iso: string): string =>
  DateUtil.parse(iso)?.setLocale(locale.value).toRelative() ?? DateUtil.formatDateTime(iso);

const diffs = computed(() =>
  Object.fromEntries(
    versions.value.map((v) => [
      v.version,
      {
        filter: lineDiff(props.currentFilter, v.filter ?? ""),
        map: lineDiff(props.currentMap, v.map ?? ""),
      },
    ]),
  ),
);

const changed = (lines: { t: string }[]): boolean => lines.some((l) => l.t !== "same");

const restore = async (v: RuleVersion): Promise<void> => {
  const ok = await confirm({
    title: t("rules.versions.restore_title", { n: v.version }),
    body: t("rules.versions.restore_body"),
    variant: "warning",
    confirmText: t("rules.versions.restore"),
  });
  if (ok) emit("restore", v.version);
};
</script>

<template>
  <AppDrawerPanel
    :model-value="modelValue"
    :title="t('rules.versions.title')"
    :width="520"
    :loading="restoring"
    @update:model-value="(v) => emit('update:modelValue', v)"
  >
    <template #prepend>
      <v-avatar color="accent">
        <v-icon :icon="mdiHistory" />
      </v-avatar>
    </template>
    <LoadingSkeleton
      v-if="load.isFirstLoad.value"
      :rows="4"
      shape="list"
    />
    <div
      v-else
      class="position-relative d-flex flex-column ga-3"
    >
      <AppSectionLoader :status="load.isRefreshing.value" />
      <p
        v-if="!versions.length"
        class="text-body-2 text-medium-emphasis"
      >
        {{ t("rules.versions.empty") }}
      </p>
      <div
        v-for="v in versions"
        :key="v.id"
        :data-testid="`rule-version-${v.version}`"
        class="version pa-3"
      >
        <div class="d-flex align-center flex-wrap ga-2">
          <span class="text-title-small font-weight-bold">{{ t("rules.versions.version", { n: v.version }) }}</span>
          <v-chip
            v-if="v.version === currentVersion"
            color="success"
          >
            {{ t("rules.versions.current") }}
          </v-chip>
          <span class="text-caption text-medium-emphasis">{{ relative(v.createdAt) }}</span>
          <v-spacer />
          <v-btn-inline
            v-if="v.version !== currentVersion"
            :prepend-icon="mdiRestore"
            :loading="restoring"
            color="warning"
            @click="restore(v)"
          >
            {{ t("rules.versions.restore") }}
          </v-btn-inline>
        </div>
        <v-btn-inline
          v-if="v.version !== currentVersion"
          class="mt-2"
          color="secondary"
          @click="open = open === v.version ? null : v.version"
        >
          {{ open === v.version ? t("rules.versions.hide_diff") : t("rules.versions.show_diff") }}
        </v-btn-inline>
        <div
          v-if="open === v.version"
          class="d-flex flex-column ga-2 mt-2"
        >
          <template
            v-for="part in (['filter', 'map'] as const)"
            :key="part"
          >
            <div v-if="changed(diffs[v.version]![part])">
              <div class="text-caption font-weight-bold mb-1">
                {{ t(`rules.test.path_${part}`) }}
              </div>
              <pre class="diff pa-2 ma-0"><span
                v-for="(l, i) in diffs[v.version]![part]"
                :key="i"
                :class="`diff--${l.t}`"
                class="d-block"
              >{{ l.t === "add" ? "+ " : l.t === "del" ? "- " : "  " }}{{ l.s }}</span></pre>
            </div>
          </template>
          <p
            v-if="!changed(diffs[v.version]!.filter) && !changed(diffs[v.version]!.map)"
            class="text-caption text-medium-emphasis mb-0"
          >
            {{ t("rules.versions.same") }}
          </p>
        </div>
      </div>
    </div>
  </AppDrawerPanel>
</template>

<style scoped lang="scss">
.version {
  border: 1px solid rgb(var(--v-border-color), var(--v-border-opacity));
  border-radius: 12px;
}

.diff {
  overflow-x: auto;
  border-radius: 10px;
  background: rgb(var(--v-theme-field));
  font-family: "JetBrains Mono", ui-monospace, monospace;
  font-size: 0.75rem;
  line-height: 1.55;
}

.diff--add {
  background: rgb(var(--v-theme-success), 0.12);
}

.diff--del {
  background: rgb(var(--v-theme-error), 0.1);
}
</style>
