import { computed, ref } from "vue";
import { getDelivery, type Delivery } from "@/api/deliveries";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";

export const useDeliveryDetail = () => {
  const { messageOf } = useProblem();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();
  const open = ref(false);
  const detail = ref<Delivery | null>(null);
  const loadError = ref<string | null>(null);

  const show = async (id: string): Promise<void> => {
    open.value = true;
    detail.value = null;
    loadError.value = null;
    start();
    try {
      detail.value = await getDelivery(id);
      finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const canReplay = computed(() => {
    const s = detail.value?.state;
    return s === "succeeded" || s === "failed" || s === "deadLettered" || s === "cancelled";
  });

  return {
    open,
    detail,
    loadError,
    canReplay,
    isFirstLoad,
    isRefreshing,
    show,
  };
};
