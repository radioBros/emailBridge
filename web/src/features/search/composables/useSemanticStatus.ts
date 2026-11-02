import { computed, ref } from "vue";
import {
  getEmbeddingsStatus,
  type EmbeddingsReason,
  type EmbeddingsStatus,
} from "@/features/search/helpers/api";

/** The i18n key explaining why semantic search is not available (no account picked yet: `undefined`). */
export const semanticReasonKey = (reason: EmbeddingsReason | undefined): string => {
  switch (reason) {
    case "fulltext_off":
      return "search.meaning_reason_fulltext_off";
    case "ai_off":
      return "search.meaning_reason_ai_off";
    case "not_configured":
      return "search.meaning_reason_not_configured";
    default:
      return "search.meaning_reason_switch_off";
  }
};

/** Whether semantic search and ask are available for one account (GET /v1/accounts/{id}/embeddings). */
export const useSemanticStatus = () => {
  const status = ref<EmbeddingsStatus | null>(null);
  const checking = ref(false);
  /** Only the answer to the latest check counts (the account can change while one is in flight). */
  let seq = 0;

  const check = async (accountId: string): Promise<void> => {
    const mine = ++seq;
    if (!accountId) {
      status.value = null;
      checking.value = false;
      return;
    }
    checking.value = true;
    try {
      const s = await getEmbeddingsStatus(accountId);
      if (mine === seq) status.value = s;
    } catch {
      // Unknown means unavailable: the toggle stays on Text and the reason line stays generic.
      if (mine === seq) status.value = null;
    } finally {
      if (mine === seq) checking.value = false;
    }
  };

  const available = computed(() => status.value?.enabled === true);
  const reasonKey = computed(() => semanticReasonKey(status.value?.reason));

  return { status, checking, available, reasonKey, check };
};
