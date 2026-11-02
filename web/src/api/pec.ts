import { api, ApiProblemError } from "@/api/client";
import type { components } from "@/api/schema";

export type Receipt = components["schemas"]["Receipt"];
export type PecReceiptList = components["schemas"]["PecreceiptsList"];
export type PECEvidenceView = components["schemas"]["PECEvidenceView"];
export type PECDefaults = components["schemas"]["PECDefaults"];
export type AutoconfigResult = components["schemas"]["Result"];
export type OutboundPECStatus = components["schemas"]["OutboundPECStatus"];
export type PECDelivery = components["schemas"]["PECDelivery"];
export type Signature = components["schemas"]["Signature"];
export type ArchiveCheck = components["schemas"]["ArchiveCheck"];

export interface PecReceiptListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  type?: string;
  outboundId?: string;
  q?: string;
  cursor?: string;
}

export type RawReceiptResult =
  | { ok: true; bytes: ArrayBuffer }
  | { ok: false; missing: true }
  | { ok: false; missing: false; error: unknown };

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

export const listAccountPecReceipts = (
  accountId: string,
  params: PecReceiptListParams = {},
): Promise<PecReceiptList> =>
  api.get<PecReceiptList>(
    `/accounts/${encodeURIComponent(accountId)}/pec/receipts${qs({
      page: params.page,
      limit: params.limit,
      includeTotal: params.includeTotal,
      sort: params.sort,
      type: params.type,
      outboundId: params.outboundId,
      q: params.q,
      cursor: params.cursor,
    })}`,
  );

export const getPecReceipt = (receiptId: string): Promise<Receipt> =>
  api.get<Receipt>(`/pec/receipts/${encodeURIComponent(receiptId)}`);

export const verifyPecReceiptEvidence = (receiptId: string): Promise<PECEvidenceView> =>
  api.get<PECEvidenceView>(`/pec/receipts/${encodeURIComponent(receiptId)}/evidence`);

export const getPecReceiptRaw = async (receiptId: string): Promise<RawReceiptResult> => {
  try {
    const res = await api.raw.get<ArrayBuffer>(
      `/pec/receipts/${encodeURIComponent(receiptId)}/raw`,
      { responseType: "arraybuffer" },
    );
    return { ok: true, bytes: res.data };
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 404) {
      return { ok: false, missing: true };
    }
    return { ok: false, missing: false, error: err };
  }
};

export const downloadPecReceiptRaw = async (
  receiptId: string,
  filename?: string,
): Promise<RawReceiptResult> => {
  const result = await getPecReceiptRaw(receiptId);
  if (!result.ok) return result;
  const name = filename?.endsWith(".eml") ? filename : `${filename || receiptId}.eml`;
  const blob = new Blob([result.bytes], { type: "message/rfc822" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
  return result;
};

/** Discover IMAP/SMTP (+ PEC presets) for an email address. */
export const getAutoconfig = (email: string): Promise<AutoconfigResult> =>
  api.get<AutoconfigResult>(`/autoconfig${qs({ email })}`);
