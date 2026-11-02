import { ref } from "vue";
import { getDraft, listDrafts, type DraftView } from "@/api/messages";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";

/** Drafts list + read-only open. */
export const useDrafts = () => {
  const { messageOf } = useProblem();
  const listLoad = useLoadState();
  const detailLoad = useLoadState();

  const items = ref<DraftView[]>([]);
  const listError = ref<string | null>(null);
  const detail = ref<DraftView | null>(null);
  const detailError = ref<string | null>(null);
  const open = ref(false);

  const load = async (accountId: string): Promise<void> => {
    if (!accountId) {
      items.value = [];
      return;
    }
    listLoad.start();
    listError.value = null;
    try {
      const res = await listDrafts(accountId, { page: 1, limit: 50 });
      items.value = res.data ?? [];
      listLoad.finish(true);
    } catch (err) {
      listError.value = messageOf(err);
      items.value = [];
      listLoad.finish(false);
    }
  };

  const show = async (accountId: string, draftId: string): Promise<void> => {
    open.value = true;
    detailLoad.start();
    detailError.value = null;
    detail.value = null;
    try {
      detail.value = await getDraft(accountId, draftId, { text: true });
      detailLoad.finish(true);
    } catch (err) {
      detailError.value = messageOf(err);
      detailLoad.finish(false);
    }
  };

  const close = (): void => {
    open.value = false;
    detail.value = null;
    detailError.value = null;
  };

  return {
    items,
    listError,
    detail,
    detailError,
    open,
    isListFirstLoad: listLoad.isFirstLoad,
    isListRefreshing: listLoad.isRefreshing,
    isDetailFirstLoad: detailLoad.isFirstLoad,
    isDetailRefreshing: detailLoad.isRefreshing,
    load,
    show,
    close,
  };
};
