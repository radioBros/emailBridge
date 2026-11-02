<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import { useI18n } from "vue-i18n";
import { mdiBrain } from "@mdi/js";
import { problemOf } from "@/api/problems";
import { actionOf } from "@/constants/Actions";
import { useConfirm } from "@/composables/useConfirm";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { reindexEmbeddings } from "@/features/search/helpers/api";
import { useSemanticStatus } from "@/features/search/composables/useSemanticStatus";

const props = defineProps<{
  accountId: string;
  /** Re-check when the account's switches change (the parent passes its version or switches). */
  revision?: string | number;
}>();

const { t } = useI18n();
const confirm = useConfirm();
const { messageOf } = useProblem();
const notify = useNotifyStore();
const syncAction = actionOf("sync");
const { status, available, check } = useSemanticStatus();

const busy = ref(false);

onMounted(() => {
  void check(props.accountId);
});

watch(
  () => [props.accountId, props.revision],
  () => {
    void check(props.accountId);
  },
);

const reindex = async (): Promise<void> => {
  const ok = await confirm({
    title: t("search.embed_confirm_title"),
    body: t("search.embed_confirm_body"),
    variant: "info",
    confirmText: t("search.embed_confirm"),
  });
  if (!ok) return;
  busy.value = true;
  try {
    await reindexEmbeddings(props.accountId);
    notify.push({ message: t("search.embed_queued"), color: "success" });
  } catch (err) {
    notify.push({ message: messageOf(problemOf(err) ?? err), color: "error" });
  } finally {
    busy.value = false;
  }
};
</script>

<template>
  <div
    v-if="available"
    class="d-flex flex-wrap align-center justify-space-between ga-3 pt-2"
    data-testid="semantic-reindex"
  >
    <div class="min-width-0">
      <div class="text-body-medium font-weight-medium">
        {{ t("search.embed_title") }}
      </div>
      <div class="text-body-small text-medium-emphasis">
        {{ t("search.embed_hint", { messages: status?.messages ?? 0, indexed: status?.indexedMessages ?? 0 }) }}
      </div>
    </div>
    <v-btn
      :loading="busy"
      :color="syncAction.color"
      :prepend-icon="mdiBrain"
      data-testid="semantic-reindex-button"
      @click="reindex"
    >
      {{ t("search.embed_action") }}
    </v-btn>
  </div>
</template>
