import { ref, type Ref } from "vue";
import { listQueueSummaries, type QueueScope, type QueueSummary } from "@/api/queues";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";

/** GET /v1/queues for the header cards: skeleton on the first load, overlay on refetch. */
export const useQueueSummaries = (scope: Ref<QueueScope>) => {
  const { messageOf } = useProblem();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
  const summaries = ref<QueueSummary[]>([]);
  const error = ref<string | null>(null);

  const load = async (): Promise<void> => {
    start();
    error.value = null;
    try {
      summaries.value = await listQueueSummaries(scope.value);
      finish(true);
    } catch (err) {
      error.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  return { summaries, error, isFirstLoad, isRefreshing, load };
};
