import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";
import { nextTick, ref } from "vue";

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

const listAccountPecReceipts = vi.fn();
const getPecReceipt = vi.fn();
const verifyPecReceiptEvidence = vi.fn();
const downloadPecReceiptRaw = vi.fn();
const getAutoconfig = vi.fn();

vi.mock("@/api/pec", () => ({
  listAccountPecReceipts: (...a: unknown[]) => listAccountPecReceipts(...a),
  getPecReceipt: (...a: unknown[]) => getPecReceipt(...a),
  verifyPecReceiptEvidence: (...a: unknown[]) => verifyPecReceiptEvidence(...a),
  downloadPecReceiptRaw: (...a: unknown[]) => downloadPecReceiptRaw(...a),
  getAutoconfig: (...a: unknown[]) => getAutoconfig(...a),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : String(e)),
    fieldErrors: () => ({}),
  }),
}));

import { usePecDiscovery } from "@/features/pec/composables/usePecDiscovery";
import { usePecReceiptDetail } from "@/features/pec/composables/usePecReceiptDetail";
import {
  sortParam,
  usePecReceipts,
} from "@/features/pec/composables/usePecReceipts";

describe("usePecReceipts", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    listAccountPecReceipts.mockReset();
  });

  it("sortParam defaults and flips", () => {
    expect(sortParam([])).toBe("-at");
    expect(sortParam([{ key: "type", order: "asc" }])).toBe("type");
    expect(sortParam([{ key: "at", order: "desc" }])).toBe("-at");
    expect(sortParam([{ key: "nope", order: "asc" }])).toBe("-at");
  });

  it("loads receipts with filters", async () => {
    listAccountPecReceipts.mockResolvedValue({
      data: [
        {
          id: "rcp_1",
          accountId: "acc_1",
          at: "2026-10-26T10:00:00Z",
          createdAt: "2026-10-26T10:00:00Z",
          messageId: "msg_1",
          outboundId: "out_1",
          sha256: "abc",
          signature: { valid: true, trusted: true },
          transportId: "tid.1",
          type: "accettazione",
        },
      ],
      total: 1,
      nextCursor: null,
    });
    const api = usePecReceipts(() => "acc_1");
    await api.load();
    expect(listAccountPecReceipts).toHaveBeenCalledWith(
      "acc_1",
      expect.objectContaining({
        page: 1,
        includeTotal: true,
        sort: "-at",
      }),
    );
    expect(api.rows.value).toHaveLength(1);
    expect(api.total.value).toBe(1);

    await api.setTypeFilter("accettazione");
    expect(listAccountPecReceipts).toHaveBeenLastCalledWith(
      "acc_1",
      expect.objectContaining({ type: "accettazione", page: 1 }),
    );

    await api.setSearch("tid");
    expect(listAccountPecReceipts).toHaveBeenLastCalledWith(
      "acc_1",
      expect.objectContaining({ q: "tid" }),
    );
  });

  it("records load errors", async () => {
    listAccountPecReceipts.mockRejectedValue(new Error("boom"));
    const api = usePecReceipts(() => "acc_1");
    await api.load();
    expect(api.loadError.value).toBe("boom");
  });
});

describe("usePecReceiptDetail", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    getPecReceipt.mockReset();
    verifyPecReceiptEvidence.mockReset();
    downloadPecReceiptRaw.mockReset();
  });

  it("loads a receipt and verifies evidence", async () => {
    getPecReceipt.mockResolvedValue({
      id: "rcp_1",
      accountId: "acc_1",
      at: "2026-10-26T10:00:00Z",
      createdAt: "2026-10-26T10:00:00Z",
      messageId: "msg_1",
      outboundId: null,
      sha256: "abc",
      signature: { valid: true, trusted: true },
      transportId: "tid.1",
      type: "accettazione",
    });
    verifyPecReceiptEvidence.mockResolvedValue({
      key: "k",
      manifestKey: "m",
      receiptId: "rcp_1",
      sha256: "abc",
      verification: {
        intact: true,
        hashMatches: true,
        locked: true,
        lockedUntil: "2036-10-26T00:00:00Z",
        tsa: { valid: true, at: "2026-10-26T10:01:00Z" },
      },
    });
    downloadPecReceiptRaw.mockResolvedValue({ ok: true, bytes: new ArrayBuffer(8) });

    const api = usePecReceiptDetail();
    await api.show("rcp_1");
    expect(api.receipt.value?.id).toBe("rcp_1");
    expect(api.open.value).toBe(true);

    await api.verifyEvidence();
    expect(api.evidence.value?.verification.intact).toBe(true);

    expect(await api.downloadRaw()).toBe(true);
  });

  it("treats missing evidence as empty, not hard error", async () => {
    getPecReceipt.mockResolvedValue({
      id: "rcp_1",
      accountId: "acc_1",
      at: "2026-10-26T10:00:00Z",
      createdAt: "2026-10-26T10:00:00Z",
      messageId: "msg_1",
      outboundId: null,
      sha256: "abc",
      signature: { valid: true, trusted: false },
      transportId: "tid.1",
      type: "accettazione",
    });
    verifyPecReceiptEvidence.mockRejectedValue({
      problem: { status: 404, code: "not_found" },
    });
    const api = usePecReceiptDetail();
    await api.show("rcp_1");
    await api.verifyEvidence();
    expect(api.evidenceMissing.value).toBe(true);
    expect(api.evidenceError.value).toBeNull();
  });
});

describe("usePecDiscovery", () => {
  beforeEach(() => {
    getAutoconfig.mockReset();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("shows banner when autoconfig reports pec", async () => {
    getAutoconfig.mockResolvedValue({
      email: "a@pec.example.it",
      domain: "pec.example.it",
      source: "preset",
      pec: true,
      pecDefaults: {
        sendPolicy: "atMostOnce",
        switches: { "pec.enabled": true },
        syncPaths: ["INBOX"],
      },
    });
    const email = ref("a@pec.example.it");
    const api = usePecDiscovery(email);
    await vi.advanceTimersByTimeAsync(500);
    await nextTick();
    expect(getAutoconfig).toHaveBeenCalledWith("a@pec.example.it");
    expect(api.showBanner.value).toBe(true);
    api.markApplied("enabled");
    expect(api.applied.value.enabled).toBe(true);
  });

  it("clears discovery for invalid email", async () => {
    const email = ref("not-an-email");
    const api = usePecDiscovery(email);
    await vi.advanceTimersByTimeAsync(500);
    expect(getAutoconfig).not.toHaveBeenCalled();
    expect(api.showBanner.value).toBe(false);
  });
});
