import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn();
const rawGet = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: (...a: unknown[]) => get(...a),
    raw: { get: (...a: unknown[]) => rawGet(...a) },
  },
  ApiProblemError: class ApiProblemError extends Error {
    problem: { status: number; code: string };
    constructor(problem: { status: number; code: string }) {
      super(problem.code);
      this.problem = problem;
    }
  },
}));

import {
  downloadPecReceiptRaw,
  getAutoconfig,
  getPecReceipt,
  getPecReceiptRaw,
  listAccountPecReceipts,
  verifyPecReceiptEvidence,
} from "@/api/pec";

describe("pec api", () => {
  beforeEach(() => {
    get.mockReset();
    rawGet.mockReset();
  });

  it("lists and fetches receipts", async () => {
    get.mockResolvedValueOnce({ data: [], total: 0, nextCursor: null });
    await listAccountPecReceipts("acc_1", {
      page: 1,
      limit: 25,
      includeTotal: true,
      sort: "-at",
      type: "accettazione",
      q: "tid",
    });
    expect(get).toHaveBeenCalledWith(
      expect.stringContaining("/accounts/acc_1/pec/receipts?"),
    );
    expect(get.mock.calls[0][0]).toContain("type=accettazione");
    expect(get.mock.calls[0][0]).toContain("q=tid");

    get.mockResolvedValueOnce({ id: "rcp_1" });
    await getPecReceipt("rcp_1");
    expect(get).toHaveBeenLastCalledWith("/pec/receipts/rcp_1");

    get.mockResolvedValueOnce({ receiptId: "rcp_1", verification: { intact: true } });
    await verifyPecReceiptEvidence("rcp_1");
    expect(get).toHaveBeenLastCalledWith("/pec/receipts/rcp_1/evidence");

    get.mockResolvedValueOnce({ pec: true, email: "a@b.it", domain: "b.it", source: "x" });
    await getAutoconfig("a@b.it");
    expect(get).toHaveBeenLastCalledWith("/autoconfig?email=a%40b.it");
  });

  it("downloads raw receipt bytes", async () => {
    rawGet.mockResolvedValue({ data: new ArrayBuffer(4) });
    const click = vi.fn();
    const orig = document.createElement.bind(document);
    vi.spyOn(document, "createElement").mockImplementation((tag: string) => {
      const el = orig(tag);
      if (tag === "a") {
        Object.defineProperty(el, "click", { value: click });
      }
      return el;
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);

    const ok = await downloadPecReceiptRaw("rcp_1", "rcp_1.eml");
    expect(ok.ok).toBe(true);
    expect(click).toHaveBeenCalled();

    const { ApiProblemError } = await import("@/api/client");
    rawGet.mockRejectedValue(new ApiProblemError({ status: 404, code: "not_found" }));
    const missing = await getPecReceiptRaw("rcp_missing");
    expect(missing).toEqual({ ok: false, missing: true });
  });
});
