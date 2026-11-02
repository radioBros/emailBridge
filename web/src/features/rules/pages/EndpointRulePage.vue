<script setup lang="ts">
/** Webhook endpoint → "Filter & transform": the per-endpoint CEL rule (W73). */
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { useI18n } from "vue-i18n";
import { mdiArrowLeft, mdiWebhook } from "@mdi/js";
import { getWebhookEndpoint, type EndpointView } from "@/api/webhooks";
import {
  restoreRuleVersion,
  saveEndpointRule,
  type EndpointRule,
} from "@/api/rules";
import { useConfirm } from "@/composables/useConfirm";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import RuleEditor from "@/features/rules/components/RuleEditor.vue";
import RuleEditorSkeleton from "@/features/rules/components/RuleEditorSkeleton.vue";
import { useLeaveGuard } from "@/features/rules/composables/useLeaveGuard";

type Endpoint = EndpointView & { rule?: EndpointRule | null };

const route = useRoute();
const { t } = useI18n();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const confirm = useConfirm();
const load = useLoadState();

const id = computed(() => String(route.params.endpointId ?? ""));
const endpoint = ref<Endpoint | null>(null);
const loadError = ref<string | null>(null);
const saving = ref(false);
const revision = ref(0);
const dirty = ref(false);

useLeaveGuard(dirty);

const fetch = async (): Promise<void> => {
  load.start();
  try {
    endpoint.value = (await getWebhookEndpoint(id.value)) as Endpoint;
    loadError.value = null;
    load.finish(true);
  } catch (e) {
    loadError.value = messageOf(e);
    load.finish(false);
  }
};

onMounted(fetch);

const accountHint = computed(() => endpoint.value?.filter?.accountIds?.[0] ?? "");

const onSave = async (rule: EndpointRule, events: string[] | null): Promise<void> => {
  if (!endpoint.value) return;
  saving.value = true;
  try {
    const body: EndpointRule = rule.filter || rule.map ? rule : {};
    endpoint.value = await saveEndpointRule<Endpoint>(id.value, body, endpoint.value.version, events);
    dirty.value = false;
    revision.value += 1;
    notify.push({ message: t("rules.saved"), color: "success" });
  } catch (e) {
    notify.push({ message: messageOf(e), color: "error" });
  } finally {
    saving.value = false;
  }
};

const onRemove = async (): Promise<void> => {
  if (!endpoint.value) return;
  const ok = await confirm({
    title: t("rules.remove_title"),
    body: t("rules.remove_body"),
    variant: "danger",
    confirmText: t("rules.remove_rule"),
  });
  if (!ok) return;
  await onSave({}, null);
};

const onRestore = async (version: number): Promise<void> => {
  if (!endpoint.value) return;
  saving.value = true;
  try {
    endpoint.value = await restoreRuleVersion<Endpoint>("endpoint", id.value, version, endpoint.value.version);
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
        to="/webhooks"
        color="primary"
      >
        {{ t("rules.back_webhooks") }}
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
      v-else-if="endpoint"
      :target-id="endpoint.id"
      :rule="endpoint.rule ?? null"
      :subscribed="endpoint.events ?? []"
      :account-hint="accountHint"
      :saving="saving"
      :revision="revision"
      :current-version="endpoint.rule?.currentVersion ?? 0"
      kind="endpoint"
      removable
      @update:dirty="(d) => (dirty = d)"
      @save="onSave"
      @remove="onRemove"
      @restore="onRestore"
    >
      <template #title>
        <div class="d-flex align-center ga-3 min-w-0">
          <v-avatar color="primary">
            <v-icon :icon="mdiWebhook" />
          </v-avatar>
          <div class="min-w-0">
            <h1 class="text-h5 font-weight-bold mb-0">
              {{ t("rules.page_title") }}
            </h1>
            <p class="text-body-medium text-medium-emphasis mb-0 text-truncate">
              {{ endpoint.name }}<template v-if="endpoint.url">
                · {{ endpoint.url }}
              </template>
            </p>
          </div>
        </div>
      </template>
    </RuleEditor>
  </div>
</template>
