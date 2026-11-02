import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import type { ServerTableSortItem } from "@/components/tables/types";
import {
  batchMessages,
  listMailboxes,
  listMessages,
  searchAccounts,
  searchMessages,
  type AccountView,
  type MailboxView,
  type MessageView,
} from "@/api/messages";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { rememberAccounts } from "@/features/messages/helpers/accountCache";
import {
  FLAG_FLAGGED,
  FLAG_SEEN,
  listFlagParam,
  type FlagFilter,
} from "@/features/messages/helpers/flags";
import { buildFolderTree, flattenFolderTree } from "@/features/messages/helpers/folders";

/** UI column `date` sorts by index `internalDate` (API whitelist). */
const SORT_MAP: Record<string, string> = {
  date: "internalDate",
  internalDate: "internalDate",
  size: "size",
  uid: "uid",
  id: "id",
};

const sortParam = (sortBy: ServerTableSortItem[]): string => {
  const first = sortBy[0];
  const field = first ? SORT_MAP[first.key] : undefined;
  if (!first || !field) return "-internalDate";
  return first.order === "desc" ? `-${field}` : field;
};

const readQuery = (raw: unknown): string => (typeof raw === "string" ? raw : "");

/** Account → folders → message list with filters and bulk actions. */
export const useMessagesBrowser = () => {
  const route = useRoute();
  const router = useRouter();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const foldersLoad = useLoadState();
  const listLoad = useLoadState();

  const accountId = ref(readQuery(route.query.accountId));
  const mailboxId = ref(readQuery(route.query.mailboxId));
  const messageId = ref(readQuery(route.query.messageId));
  const flagFilter = ref<FlagFilter>((readQuery(route.query.flag) as FlagFilter) || "");

  const accountOptions = ref<AccountView[]>([]);
  const accountsLoading = ref(false);
  const mailboxes = ref<MailboxView[]>([]);
  const foldersError = ref<string | null>(null);

  const items = ref<MessageView[]>([]);
  const total = ref(0);
  const loadError = ref<string | null>(null);
  const page = ref(Number(route.query.page) || 1);
  const ipp = ref(Number(route.query.limit) || 25);
  const search = ref(readQuery(route.query.q));
  const sortBy = ref<ServerTableSortItem[]>([{ key: "date", order: "desc" }]);
  const selected = ref<string[]>([]);
  const bulkBusy = ref(false);

  /** Mobile step: folders | list | reader */
  const mobileStep = ref<"folders" | "list" | "reader">(
    messageId.value ? "reader" : mailboxId.value ? "list" : "folders",
  );

  const folderNodes = computed(() => flattenFolderTree(buildFolderTree(mailboxes.value)));
  const selectedMailbox = computed(
    () => mailboxes.value.find((m) => m.id === mailboxId.value) ?? null,
  );

  const hasActiveFilters = computed(
    () => Boolean(search.value) || Boolean(flagFilter.value),
  );

  const empty = computed(
    () =>
      !listLoad.isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      Boolean(mailboxId.value) &&
      !hasActiveFilters.value,
  );

  const noResults = computed(
    () =>
      !listLoad.isLoading.value &&
      !loadError.value &&
      items.value.length === 0 &&
      Boolean(mailboxId.value) &&
      hasActiveFilters.value,
  );

  const syncUrl = (): void => {
    const query: Record<string, string> = {};
    if (accountId.value) query.accountId = accountId.value;
    if (mailboxId.value) query.mailboxId = mailboxId.value;
    if (messageId.value) query.messageId = messageId.value;
    if (page.value > 1) query.page = String(page.value);
    if (ipp.value !== 25) query.limit = String(ipp.value);
    if (search.value) query.q = search.value;
    if (flagFilter.value) query.flag = flagFilter.value;
    void router.replace({ query });
  };

  const loadAccounts = async (q = ""): Promise<AccountView[]> => {
    accountsLoading.value = true;
    try {
      const rows = await searchAccounts(q);
      rememberAccounts(rows);
      accountOptions.value = rows;
      return rows;
    } catch (err) {
      // Used to reject unhandled and abort the page boot (no account, no folders, no message).
      // Notified rather than written to `loadError`, so a failed search never blanks the list.
      notify.push({ message: messageOf(err), color: "error" });
      return [];
    } finally {
      accountsLoading.value = false;
    }
  };

  const loadFolders = async (): Promise<void> => {
    if (!accountId.value) {
      mailboxes.value = [];
      return;
    }
    foldersLoad.start();
    foldersError.value = null;
    try {
      const res = await listMailboxes(accountId.value);
      mailboxes.value = res.data ?? [];
      foldersLoad.finish(true);
    } catch (err) {
      foldersError.value = messageOf(err);
      mailboxes.value = [];
      foldersLoad.finish(false);
    }
  };

  const loadMessages = async (): Promise<void> => {
    if (!accountId.value || !mailboxId.value) {
      items.value = [];
      total.value = 0;
      return;
    }
    listLoad.start();
    loadError.value = null;
    selected.value = [];
    try {
      if (flagFilter.value === "unread") {
        const res = await searchMessages(accountId.value, {
          mailboxId: mailboxId.value,
          unseen: true,
        });
        let rows = res.data ?? [];
        if (search.value) {
          const q = search.value.toLowerCase();
          rows = rows.filter(
            (m) =>
              (m.messageIdHeader ?? "").toLowerCase().includes(q) ||
              (m.emailId ?? "").toLowerCase().includes(q),
          );
        }
        const start = (page.value - 1) * ipp.value;
        total.value = rows.length;
        items.value = rows.slice(start, start + ipp.value);
      } else {
        const res = await listMessages(accountId.value, {
          mailboxId: mailboxId.value,
          flag: listFlagParam(flagFilter.value),
          q: search.value || undefined,
          page: page.value,
          limit: ipp.value,
          sort: sortParam(sortBy.value),
          includeTotal: true,
        });
        items.value = res.data ?? [];
        total.value = res.total ?? items.value.length;
      }
      listLoad.finish(true);
    } catch (err) {
      loadError.value = messageOf(err);
      items.value = [];
      total.value = 0;
      listLoad.finish(false);
    }
  };

  const selectAccount = async (id: string): Promise<void> => {
    accountId.value = id;
    mailboxId.value = "";
    messageId.value = "";
    page.value = 1;
    selected.value = [];
    mobileStep.value = "folders";
    syncUrl();
    await loadFolders();
  };

  const selectFolder = (id: string): void => {
    mailboxId.value = id;
    messageId.value = "";
    page.value = 1;
    selected.value = [];
    mobileStep.value = "list";
    syncUrl();
    void loadMessages();
  };

  const openMessage = (id: string): void => {
    messageId.value = id;
    mobileStep.value = "reader";
    syncUrl();
  };

  const clearMessage = (): void => {
    messageId.value = "";
    mobileStep.value = "list";
    syncUrl();
  };

  const backToFolders = (): void => {
    messageId.value = "";
    mailboxId.value = "";
    mobileStep.value = "folders";
    syncUrl();
  };

  const runBatch = async (
    action: "flags" | "move" | "delete",
    extra: Partial<{
      addFlags: string[];
      removeFlags: string[];
      mailboxId: string;
    }> = {},
  ): Promise<boolean> => {
    if (!accountId.value || !selected.value.length) return false;
    bulkBusy.value = true;
    try {
      await batchMessages(accountId.value, {
        ids: [...selected.value],
        action,
        ...extra,
      });
      selected.value = [];
      await loadMessages();
      return true;
    } catch (err) {
      loadError.value = messageOf(err);
      return false;
    } finally {
      bulkBusy.value = false;
    }
  };

  const bulkMarkRead = (): Promise<boolean> =>
    runBatch("flags", { addFlags: [FLAG_SEEN] });
  const bulkMarkUnread = (): Promise<boolean> =>
    runBatch("flags", { removeFlags: [FLAG_SEEN] });
  const bulkFlag = (): Promise<boolean> =>
    runBatch("flags", { addFlags: [FLAG_FLAGGED] });
  const bulkUnflag = (): Promise<boolean> =>
    runBatch("flags", { removeFlags: [FLAG_FLAGGED] });
  const bulkDelete = (): Promise<boolean> => runBatch("delete");
  const bulkMove = (targetMailboxId: string): Promise<boolean> =>
    runBatch("move", { mailboxId: targetMailboxId });

  watch(
    [page, ipp, search, sortBy, flagFilter],
    () => {
      syncUrl();
      void loadMessages();
    },
    { deep: true },
  );

  /** Keep pane selection in sync when the URL is driven externally (shots, deep links). */
  watch(
    () => route.query,
    (q) => {
      const nextAccount = readQuery(q.accountId);
      const nextMailbox = readQuery(q.mailboxId);
      const nextMessage = readQuery(q.messageId);
      const accountChanged = nextAccount !== accountId.value;
      const mailboxChanged = nextMailbox !== mailboxId.value;
      const messageChanged = nextMessage !== messageId.value;
      if (!accountChanged && !mailboxChanged && !messageChanged) return;
      accountId.value = nextAccount;
      mailboxId.value = nextMailbox;
      messageId.value = nextMessage;
      if (nextMessage) mobileStep.value = "reader";
      else if (nextMailbox) mobileStep.value = "list";
      else mobileStep.value = "folders";
      if (accountChanged && nextAccount) {
        void loadFolders();
      }
      if ((accountChanged || mailboxChanged) && nextAccount && nextMailbox) {
        void loadMessages();
      }
    },
  );

  return {
    accountId,
    mailboxId,
    messageId,
    flagFilter,
    accountOptions,
    accountsLoading,
    mailboxes,
    folderNodes,
    selectedMailbox,
    foldersError,
    isFoldersFirstLoad: foldersLoad.isFirstLoad,
    isFoldersRefreshing: foldersLoad.isRefreshing,
    items,
    total,
    page,
    ipp,
    search,
    sortBy,
    selected,
    bulkBusy,
    loadError,
    empty,
    noResults,
    mobileStep,
    isListFirstLoad: listLoad.isFirstLoad,
    isListRefreshing: listLoad.isRefreshing,
    loadAccounts,
    loadFolders,
    loadMessages,
    selectAccount,
    selectFolder,
    openMessage,
    clearMessage,
    backToFolders,
    bulkMarkRead,
    bulkMarkUnread,
    bulkFlag,
    bulkUnflag,
    bulkDelete,
    bulkMove,
    syncUrl,
  };
};
