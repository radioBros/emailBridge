import { computed, ref } from "vue";
import { useI18n } from "vue-i18n";
import {
  deleteSandboxMessage,
  downloadSandboxRaw,
  getSandboxMessage,
  getSandboxMessageRaw,
  type SandboxView,
} from "@/api/sandbox";
import {
  bytesToUtf8,
  parseMime,
  type MimeParseResult,
} from "@/features/sandbox/helpers/mimeParse";
import { useLoadState } from "@/composables/useLoadState";
import { problemOf } from "@/api/problems";
import { useProblem } from "@/composables/useProblem";
import { useNotifyStore } from "@/stores/notify";

/** Viewer drawer: metadata + client-side MIME parse of raw body. */
export const useSandboxViewer = () => {
  const { t } = useI18n();
  const { messageOf } = useProblem();
  const notify = useNotifyStore();
  const { isFirstLoad, isRefreshing, start, finish, reset } = useLoadState();

  const open = ref(false);
  const detail = ref<SandboxView | null>(null);
  const parsed = ref<MimeParseResult | null>(null);
  const rawText = ref<string | null>(null);
  const rawMissing = ref(false);
  const loadError = ref<string | null>(null);
  const loadImages = ref(false);
  const deleting = ref(false);
  const downloading = ref(false);

  const hasHtml = computed(() => Boolean(parsed.value?.html));
  const hasText = computed(() => Boolean(parsed.value?.text));

  const show = async (id: string): Promise<void> => {
    open.value = true;
    detail.value = null;
    parsed.value = null;
    rawText.value = null;
    rawMissing.value = false;
    loadImages.value = false;
    reset();
    start();
    loadError.value = null;
    try {
      detail.value = await getSandboxMessage(id);
      const raw = await getSandboxMessageRaw(id);
      if (raw.ok) {
        const text = bytesToUtf8(raw.bytes);
        rawText.value = text;
        parsed.value = parseMime(text);
      } else if (raw.missing) {
        rawMissing.value = true;
      } else {
        loadError.value = messageOf(problemOf(raw.error) ?? raw.error);
      }
      finish(true);
    } catch (err) {
      loadError.value = messageOf(problemOf(err) ?? err);
      finish(false);
    }
  };

  const close = (): void => {
    open.value = false;
    detail.value = null;
    parsed.value = null;
    rawText.value = null;
  };

  const download = async (): Promise<void> => {
    if (!detail.value) return;
    downloading.value = true;
    try {
      const result = await downloadSandboxRaw(detail.value.id, detail.value.id);
      if (!result.ok && result.missing) {
        notify.push({ message: t("sandbox.raw_missing"), color: "warning" });
      } else if (!result.ok) {
        notify.push({
          message: messageOf(problemOf(result.error) ?? result.error),
          color: "error",
        });
      }
    } finally {
      downloading.value = false;
    }
  };

  const remove = async (): Promise<boolean> => {
    if (!detail.value) return false;
    deleting.value = true;
    try {
      await deleteSandboxMessage(detail.value.id);
      notify.push({ message: t("sandbox.deleted"), color: "success" });
      close();
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
      deleting.value = false;
    }
  };

  return {
    open,
    detail,
    parsed,
    rawText,
    rawMissing,
    loadError,
    loadImages,
    deleting,
    downloading,
    hasHtml,
    hasText,
    isFirstLoad,
    isRefreshing,
    show,
    close,
    download,
    remove,
  };
};
