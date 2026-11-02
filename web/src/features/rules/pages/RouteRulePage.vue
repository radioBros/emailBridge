<script setup lang="ts">
/** Route → "Filter & transform": the same editor for a fan-out route's filter and map (S8 / W73). */
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { useI18n } from "vue-i18n";
import { mdiArrowLeft, mdiRouter } from "@mdi/js";
import { getRoute, patchRoute, type Route, type RouteUpdate } from "@/api/routes";
import { getWebhookEndpoint } from "@/api/webhooks";
import { restoreRuleVersion, type EndpointRule } from "@/api/rules";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import RuleEditor from "@/features/rules/components/RuleEditor.vue";
import RuleEditorSkeleton from "@/features/rules/components/RuleEditorSkeleton.vue";
import { useLeaveGuard } from "@/features/rules/composables/useLeaveGuard";

const route = useRoute();
const { t } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const load = useLoadState();

const id = computed(() => String(route.params.routeId ?? ""));
const target = ref<Route | null>(null);
const endpointName = ref("");
const loadError = ref<string | null>(null);
const saving = ref(false);
const revision = ref(0);
const dirty = ref(false);

useLeaveGuard(dirty);

const fetch = async (): Promise<void> => {
  load.start();
  try {
    target.value = await getRoute(id.value);
    loadError.value = null;
    load.finish(true);
    getWebhookEndpoint(target.value.endpointId)
      .then((ep) => (endpointName.value = ep.name))
      .catch(() => (endpointName.value = ""));
  } catch (e) {
    loadError.value = messageOf(e);
    load.finish(false);
  }
};

onMounted(fetch);

const rule = computed((): EndpointRule | null =>
  target.value
    ? {
        filter: target.value.filter,
        map: target.value.map ?? "",
        builder: target.value.builder as EndpointRule["builder"],
        currentVersion: target.value.currentVersion,
      }
    : null,
);

const onSave = async (draft: EndpointRule): Promise<void> => {
  if (!target.value) return;
  saving.value = true;
  try {
    const body: RouteUpdate = {
      filter: draft.filter || "true",
      map: draft.map ?? "",
      builder: (draft.builder ?? undefined) as RouteUpdate["builder"],
    };
    target.value = await patchRoute(id.value, body);
    dirty.value = false;
    revision.value += 1;
    notify.push({ message: t("rules.saved"), color: "success" });
  } catch (e) {
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    saving.value = false;
  }
};

const onRestore = async (version: number): Promise<void> => {
  if (!target.value) return;
  saving.value = true;
  try {
    target.value = await restoreRuleVersion<Route>("route", id.value, version, target.value.version);
    dirty.value = false;
    revision.value += 1;
    notify.push({ message: t("rules.versions.restored", { n: version }), color: "success" });
  } catch (e) {
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    saving.value = false;
  }
};
</script>

<template>
  <div class="d-flex flex-column ga-4">
    <div>
      <v-btn-inline
        :prepend-icon="mdiArrowLeft"
        to="/routes"
        color="primary"
      >
        {{ t("rules.back_routes") }}
      </v-btn-inline>
    </div>

    <v-alert
      v-if="loadError"
      type="error"
    >
      {{ loadError }}
      <template #append>
        <v-btn
          color="error"
          @click="fetch"
        >
          {{ t("rules.retry") }}
        </v-btn>
      </template>
    </v-alert>

    <RuleEditorSkeleton v-if="load.isFirstLoad.value" />

    <RuleEditor
      v-else-if="target"
      :target-id="target.id"
      :rule="rule"
      :saving="saving"
      :revision="revision"
      :current-version="target.currentVersion"
      kind="route"
      @update:dirty="(d) => (dirty = d)"
      @save="onSave"
      @restore="onRestore"
    >
      <template #title>
        <div class="d-flex align-center ga-3 min-w-0">
          <v-avatar color="accent">
            <v-icon :icon="mdiRouter" />
          </v-avatar>
          <div class="min-w-0">
            <h1 class="text-h5 font-weight-bold mb-0">
              {{ t("rules.page_title") }}
            </h1>
            <p class="text-body-medium text-medium-emphasis mb-0 text-truncate">
              {{ target.name }}<template v-if="endpointName">
                → {{ endpointName }}
              </template>
            </p>
          </div>
        </div>
      </template>
    </RuleEditor>
  </div>
</template>
