import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  deleteMessage,
  downloadAttachment,
  downloadMessageRaw,
  getMessage,
  getMessageRaw,
  listMessages,
  moveMessage,
  patchMessage,
  type AttachmentView,
  type MessageView,
} from "@/api/messages";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";
import { FLAG_FLAGGED, FLAG_SEEN, isFlagged, isUnread } from "@/features/messages/helpers/flags";
import { bytesToText } from "@/features/messages/helpers/format";
import {
  parseMime,
  type MimeParseResult,
} from "@/features/sandbox/helpers/mimeParse";

/** Load one message (envelope + body), raw/attachments, flags, thread peers. */
export const useMessageReader = () => {
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, start, finish } = useLoadState();

  const detail = ref<MessageView | null>(null);
  const loadError = ref<string | null>(null);
  const loadImages = ref(false);
  const rawMissing = ref(false);
  const parsed = ref<MimeParseResult | null>(null);
  const attachments = ref<AttachmentView[]>([]);
  const attBlocked = ref<string | null>(null);
  const threadPeers = ref<MessageView[]>([]);
  const busy = ref(false);
  const downloading = ref(false);

  const accountId = ref("");
  const mailboxId = ref("");

  const unread = computed(() => isUnread(detail.value?.flags));
  const flagged = computed(() => isFlagged(detail.value?.flags));
  /** Only sanitised HTML is shown in the iframe — never raw `html` (XSS). */
  const hasHtml = computed(() => Boolean(detail.value?.webSafeHtml));
  const hasText = computed(() => Boolean(detail.value?.text || parsed.value?.text));
  const htmlBody = computed(() => detail.value?.webSafeHtml || "");
  const textBody = computed(() => detail.value?.text || parsed.value?.text || "");

  const loadThread = async (threadId: string | undefined): Promise<void> => {
    threadPeers.value = [];
    if (!accountId.value || !threadId || !detail.value) return;
    try {
      const res = await listMessages(accountId.value, {
        threadId,
        page: 1,
        limit: 50,
        sort: "internalDate",
        includeTotal: false,
      });
      threadPeers.value = (res.data ?? []).filter((m) => m.id !== detail.value?.id);
    } catch {
      threadPeers.value = [];
    }
  };

  const enrichFromRaw = async (messageId: string): Promise<void> => {
    const raw = await getMessageRaw(accountId.value, messageId);
    if (!raw.ok) {
      rawMissing.value = raw.missing;
      return;
    }
    rawMissing.value = false;
    const text = bytesToText(raw.bytes);
    parsed.value = parseMime(text);
  };

  const show = async (
    accId: string,
    mbxId: string,
    messageId: string,
  ): Promise<void> => {
    accountId.value = accId;
    mailboxId.value = mbxId;
    start();
    loadError.value = null;
    loadImages.value = false;
    rawMissing.value = false;
    attBlocked.value = null;
    parsed.value = null;
    attachments.value = [];
    detail.value = null;
    try {
      detail.value = await getMessage(accId, messageId, {
        text: "both",
        headers: true,
        envelope: true,
        preProcessHtml: true,
      });
      attachments.value = detail.value.attachments ?? [];
      await enrichFromRaw(messageId);
      await loadThread(detail.value.threadId);
      finish(true);
    } catch (err) {
      loadError.value = messageOf(err);
      detail.value = null;
      finish(false);
    }
  };

  const clear = (): void => {
    detail.value = null;
    loadError.value = null;
    parsed.value = null;
    attachments.value = [];
    attBlocked.value = null;
    threadPeers.value = [];
    loadImages.value = false;
  };

  const toggleSeen = async (): Promise<void> => {
    if (!detail.value || !accountId.value) return;
    busy.value = true;
    try {
      detail.value = await patchMessage(accountId.value, detail.value.id, unread.value
        ? { addFlags: [FLAG_SEEN] }
        : { removeFlags: [FLAG_SEEN] });
    } catch (err) {
      loadError.value = messageOf(err);
    } finally {
      busy.value = false;
    }
  };

  const toggleFlagged = async (): Promise<void> => {
    if (!detail.value || !accountId.value) return;
    busy.value = true;
    try {
      detail.value = await patchMessage(accountId.value, detail.value.id, flagged.value
        ? { removeFlags: [FLAG_FLAGGED] }
        : { addFlags: [FLAG_FLAGGED] });
    } catch (err) {
      loadError.value = messageOf(err);
    } finally {
      busy.value = false;
    }
  };

  const moveTo = async (targetMailboxId: string): Promise<boolean> => {
    if (!detail.value || !accountId.value) return false;
    busy.value = true;
    try {
      await moveMessage(accountId.value, detail.value.id, { mailboxId: targetMailboxId });
      return true;
    } catch (err) {
      loadError.value = messageOf(err);
      return false;
    } finally {
      busy.value = false;
    }
  };

  const remove = async (): Promise<boolean> => {
    if (!detail.value || !accountId.value) return false;
    busy.value = true;
    try {
      await deleteMessage(accountId.value, detail.value.id);
      clear();
      return true;
    } catch (err) {
      loadError.value = messageOf(err);
      return false;
    } finally {
      busy.value = false;
    }
  };

  const downloadRaw = async (): Promise<void> => {
    if (!detail.value || !accountId.value) return;
    downloading.value = true;
    try {
      const name = detail.value.messageIdHeader?.replace(/[<>]/g, "") || detail.value.id;
      const result = await downloadMessageRaw(accountId.value, detail.value.id, name);
      if (!result.ok && !result.missing) {
        loadError.value = messageOf(result.error);
      } else if (!result.ok && result.missing) {
        rawMissing.value = true;
      }
    } finally {
      downloading.value = false;
    }
  };

  const downloadPart = async (att: AttachmentView): Promise<void> => {
    if (!detail.value || !accountId.value) return;
    if (att.scan === "infected") {
      attBlocked.value = att.id;
      return;
    }
    attBlocked.value = null;
    downloading.value = true;
    try {
      const result = await downloadAttachment(
        accountId.value,
        detail.value.id,
        att.id,
        att.filename || "attachment",
      );
      if (!result.ok) {
        if (result.missing) {
          notify.push({ message: t("messages.att_missing"), color: "error" });
        } else {
          loadError.value = messageOf(result.error);
          notify.push({ message: loadError.value, color: "error" });
        }
      }
    } finally {
      downloading.value = false;
    }
  };

  return {
    detail,
    loadError,
    loadImages,
    rawMissing,
    parsed,
    attachments,
    attBlocked,
    threadPeers,
    busy,
    downloading,
    unread,
    flagged,
    hasHtml,
    hasText,
    htmlBody,
    textBody,
    isFirstLoad,
    isRefreshing,
    show,
    clear,
    toggleSeen,
    toggleFlagged,
    moveTo,
    remove,
    downloadRaw,
    downloadPart,
  };
};
