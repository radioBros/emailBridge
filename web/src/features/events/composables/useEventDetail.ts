import { computed, ref } from "vue";
import { getEvent, type EbEvent } from "@/api/events";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { ensureAccountLabels } from "@/features/events/helpers/accountCache";

export const useEventDetail = () => {
  const open = ref(false);
  const detail = ref<EbEvent | null>(null);
  const loadError = ref<string | null>(null);
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
  const { messageOf } = useProblem();

  const jsonText = computed(() =>
    detail.value ? JSON.stringify(detail.value, null, 2) : "",
  );

  const show = async (id: string): Promise<void> => {
    open.value = true;
    detail.value = null;
    loadError.value = null;
    start();
    try {
      detail.value = await getEvent(id);
      if (detail.value.ebaccount) {
        await ensureAccountLabels([detail.value.ebaccount]);
      }
      finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  return {
    open,
    detail,
    loadError,
    jsonText,
    isFirstLoad,
    isRefreshing,
    show,
  };
};
