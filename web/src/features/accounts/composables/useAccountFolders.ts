import { computed, ref, type Ref } from "vue";
import { listMailboxes, type MailboxView } from "@/api/messages";
import { problemOf } from "@/api/problems";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import {
  buildFolderTree,
  flattenFolderTree,
  type FolderNode,
} from "@/features/messages/helpers/folders";

/** Account folders (mailboxes) as a delimiter-aware tree. */
export const useAccountFolders = (accountId: Ref<string>) => {
  const { messageOf } = useProblem();
  const loadState = useLoadState();

  const mailboxes = ref<MailboxView[]>([]);
  const loadError = ref<string | null>(null);

  const nodes = computed<FolderNode[]>(() =>
    flattenFolderTree(buildFolderTree(mailboxes.value)),
  );

  const empty = computed(
    () => !loadState.isLoading.value && !loadError.value && mailboxes.value.length === 0,
  );

  const load = async (): Promise<void> => {
    const id = accountId.value;
    if (!id) return;
    loadState.start();
    loadError.value = null;
    try {
      const res = await listMailboxes(id);
      mailboxes.value = res.data ?? [];
      loadState.finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      loadState.finish(false);
    }
  };

  return {
    mailboxes,
    nodes,
    loadError,
    empty,
    isFirstLoad: loadState.isFirstLoad,
    isRefreshing: loadState.isRefreshing,
    load,
  };
};
