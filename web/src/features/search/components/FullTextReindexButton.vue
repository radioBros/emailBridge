<script setup lang="ts">
import { onMounted, ref, computed } from "vue";
import { useI18n } from "vue-i18n";
import { mdiTextSearch } from "@mdi/js";
import { getSettings } from "@/api/settings";
import { problemOf } from "@/api/problems";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { FULLTEXT_SWITCH, reindexAccount } from "@/features/search/helpers/api";

const props = defineProps<{
  accountId: string;
  /** The account set `search.fullText` to false (it opted out of full-text search). */
  optedOut?: boolean;
}>();

const { t } = useI18n();
const confirm = useConfirm();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const syncAction = actionOf("sync");

const orgOn = ref(false);
const busy = ref(false);

/** Mirrors the backend rule: the organisation switch is the master, an account can only opt out. */
const visible = computed(() => orgOn.value && !props.optedOut);

onMounted(async () => {
  try {
    const { body } = await getSettings();
    orgOn.value = body.switches?.[FULLTEXT_SWITCH]?.value === true;
  } catch {
    orgOn.value = false;
  }
});

const reindex = async (): Promise<void> => {
  const ok = await confirm({
    title: t("search.reindex_confirm_title"),
    body: t("search.reindex_confirm_body"),
    variant: "info",
    confirmText: t("search.reindex_confirm"),
  });
  if (!ok) return;
  busy.value = true;
  try {
    await reindexAccount(props.accountId);
    notify.push({ message: t("search.reindex_queued"), color: "success" });
  } catch (err) {
    notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
  } finally {
    busy.value = false;
  }
};
</script>

<template>
  <div
    v-if="visible"
    class="d-flex flex-wrap align-center justify-space-between ga-3 pt-2"
  >
    <div class="min-width-0">
      <div class="text-body-medium font-weight-medium">
        {{ t("search.reindex_title") }}
      </div>
      <div class="text-body-small text-medium-emphasis">
        {{ t("search.reindex_hint") }}
      </div>
    </div>
    <v-btn
      :loading="busy"
      :color="syncAction.color"
      :prepend-icon="mdiTextSearch"
      data-testid="fulltext-reindex"
      @click="reindex"
    >
      {{ t("search.reindex_action") }}
    </v-btn>
  </div>
</template>
