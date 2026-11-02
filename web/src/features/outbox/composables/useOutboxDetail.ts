import { computed, ref } from "vue";
import {
  cancelOutboundMessage,
  getOutboundMessage,
  type SubmitView,
} from "@/api/outbox";
import { canCancelOutbound } from "@/features/outbox/helpers/outboundState";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { useI18n } from "vue-i18n";

/** Detail drawer: fetch one outbound message and cancel when allowed. */
export const useOutboxDetail = () => {
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, start, finish, reset } = useLoadState();

  const open = ref(false);
  const detail = ref<SubmitView | null>(null);
  const loadError = ref<string | null>(null);
  const cancelling = ref(false);

  const canCancel = computed(() =>
    detail.value ? canCancelOutbound(detail.value.state) : false,
  );

  const show = async (id: string): Promise<void> => {
    open.value = true;
    detail.value = null;
    reset();
    start();
    loadError.value = null;
    try {
      detail.value = await getOutboundMessage(id);
      finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const close = (): void => {
    open.value = false;
    detail.value = null;
    loadError.value = null;
  };

  const cancel = async (): Promise<boolean> => {
    if (!detail.value || !canCancel.value) return false;
    cancelling.value = true;
    try {
      detail.value = await cancelOutboundMessage(detail.value.id, detail.value.version);
      notify.push({ message: t("outbox.cancelled"), color: "success" });
      return true;
    } catch (err) {
      notify.push({
        message: messageOf(problemOf(err) ?? err),
        color: "error",
        code: problemOf(err)?.code,
        traceId: problemOf(err)?.traceId,
      });
      return false;
    } finally {
      cancelling.value = false;
    }
  };

  return {
    open,
    detail,
    loadError,
    cancelling,
    canCancel,
    isFirstLoad,
    isRefreshing,
    show,
    close,
    cancel,
  };
};
