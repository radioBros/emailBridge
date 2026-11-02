import { computed, ref } from "vue";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { askMailbox, type AskResult } from "@/features/search/helpers/api";

/** Longest question the server accepts (internal/embeddings maxQuestionRunes). */
export const ASK_MAX_CHARS = 1000;

/** One piece of an answer: plain text, or a citation of a message the answer used. */
export type AnswerPart = { kind: "text"; text: string } | { kind: "cite"; messageId: string; n: number };

const CITE = /\[(msg_[0-9a-z]{26}(?:\s*,\s*msg_[0-9a-z]{26})*)\]/g;

/**
 * Splits an answer into text and numbered citation markers (never HTML). Each cited message gets the
 * number of its chip in `citations` order; IDs that are not among the citations stay out.
 */
export const answerParts = (answer: string, citedIds: string[]): AnswerPart[] => {
  const numberOf = new Map(citedIds.map((id, i) => [id, i + 1]));
  const parts: AnswerPart[] = [];
  let last = 0;
  for (const m of answer.matchAll(CITE)) {
    const at = m.index ?? 0;
    if (at > last) parts.push({ kind: "text", text: answer.slice(last, at) });
    for (const id of m[1].split(",").map((s) => s.trim())) {
      const n = numberOf.get(id);
      if (n) parts.push({ kind: "cite", messageId: id, n });
    }
    last = at + m[0].length;
  }
  if (last < answer.length) parts.push({ kind: "text", text: answer.slice(last) });
  return parts;
};

/** The ask panel: question, answer and its citations for one account. */
export const useAskMailbox = () => {
  const { messageOf } = useProblem();
  const state = useLoadState();
  const question = ref("");
  const result = ref<AskResult | null>(null);
  /** The question the shown answer belongs to. */
  const asked = ref("");
  const error = ref<string | null>(null);

  const canAsk = computed(() => {
    const q = question.value.trim();
    return q !== "" && q.length <= ASK_MAX_CHARS && !state.isLoading.value;
  });

  const parts = computed(() =>
    result.value ? answerParts(result.value.answer, result.value.citations.map((c) => c.messageId)) : [],
  );

  const ask = async (accountId: string, range: { from?: string; to?: string } = {}): Promise<void> => {
    const q = question.value.trim();
    if (!accountId || !q || !canAsk.value) return;
    state.start();
    error.value = null;
    try {
      result.value = await askMailbox(accountId, q, range);
      asked.value = q;
      state.finish(true);
    } catch (err) {
      error.value = messageOf(problemOf(err) ?? err);
      state.finish(false);
    }
  };

  /** Another account: the answer no longer applies. */
  const reset = (): void => {
    result.value = null;
    asked.value = "";
    error.value = null;
  };

  return {
    question,
    result,
    asked,
    error,
    parts,
    canAsk,
    // Skeleton while nothing is shown (also after the account changed), overlay over a shown answer.
    isFirstLoad: computed(() => state.isLoading.value && !result.value),
    isRefreshing: computed(() => state.isLoading.value && result.value !== null),
    isLoading: state.isLoading,
    ask,
    reset,
  };
};
