import { ref } from "vue";
import { useLoadState } from "@/composables/useLoadState";
import { useProblem } from "@/composables/useProblem";
import {
  downloadPecReceiptRaw,
  getPecReceipt,
  verifyPecReceiptEvidence,
  type PECEvidenceView,
  type Receipt,
} from "@/api/pec";

export const usePecReceiptDetail = () => {
  const { isFirstLoad, isRefreshing, start, finish, reset } = useLoadState();
  const { messageOf } = useProblem();

  const open = ref(false);
  const receipt = ref<Receipt | null>(null);
  const loadError = ref<string | null>(null);
  const evidence = ref<PECEvidenceView | null>(null);
  const evidenceError = ref<string | null>(null);
  const evidenceMissing = ref(false);
  const verifying = ref(false);
  const downloading = ref(false);
  const downloadError = ref<string | null>(null);

  const show = async (receiptId: string): Promise<void> => {
    open.value = true;
    reset();
    receipt.value = null;
    evidence.value = null;
    evidenceError.value = null;
    evidenceMissing.value = false;
    downloadError.value = null;
    start();
    loadError.value = null;
    try {
      receipt.value = await getPecReceipt(receiptId);
      finish(true);
    } catch (e) {
      loadError.value = messageOf(e);
      finish(false);
    }
  };

  const close = (): void => {
    open.value = false;
  };

  const verifyEvidence = async (): Promise<void> => {
    const id = receipt.value?.id;
    if (!id) return;
    verifying.value = true;
    evidenceError.value = null;
    evidenceMissing.value = false;
    evidence.value = null;
    try {
      evidence.value = await verifyPecReceiptEvidence(id);
    } catch (e) {
      const msg = messageOf(e);
      // 404 / no archive → calm empty state, not a hard error banner
      if (
        typeof e === "object" &&
        e !== null &&
        "problem" in e &&
        (e as { problem?: { status?: number; code?: string } }).problem?.status === 404
      ) {
        evidenceMissing.value = true;
      } else if (
        typeof e === "object" &&
        e !== null &&
        "problem" in e &&
        (e as { problem?: { code?: string } }).problem?.code === "not_found"
      ) {
        evidenceMissing.value = true;
      } else {
        evidenceError.value = msg;
      }
    } finally {
      verifying.value = false;
    }
  };

  const downloadRaw = async (): Promise<boolean> => {
    const r = receipt.value;
    if (!r) return false;
    downloading.value = true;
    downloadError.value = null;
    try {
      const result = await downloadPecReceiptRaw(r.id, `${r.id}.eml`);
      if (!result.ok) {
        downloadError.value = result.missing
          ? "pec.raw_missing"
          : messageOf(result.error);
        return false;
      }
      return true;
    } catch (e) {
      downloadError.value = messageOf(e);
      return false;
    } finally {
      downloading.value = false;
    }
  };

  return {
    open,
    receipt,
    loadError,
    evidence,
    evidenceError,
    evidenceMissing,
    verifying,
    downloading,
    downloadError,
    isFirstLoad,
    isRefreshing,
    show,
    close,
    verifyEvidence,
    downloadRaw,
  };
};
