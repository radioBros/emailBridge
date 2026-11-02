import { api, ApiProblemError } from "@/api/client";
import type { components } from "@/api/schema";
import { searchAccounts } from "@/api/outbox";

export type SandboxView = components["schemas"]["SandboxView"];
export type SandboxList = components["schemas"]["SandboxList"];

export { searchAccounts };

export interface SandboxListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  accountId?: string;
  from?: string;
  to?: string;
  q?: string;
}

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

export const listSandboxMessages = (query: SandboxListQuery = {}): Promise<SandboxList> =>
  api.get<SandboxList>(
    `/sandbox/messages${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "-capturedAt",
      accountId: query.accountId,
      from: query.from,
      to: query.to,
      q: query.q,
    })}`,
  );

export const getSandboxMessage = (captureId: string): Promise<SandboxView> =>
  api.get<SandboxView>(`/sandbox/messages/${encodeURIComponent(captureId)}`);

export const deleteSandboxMessage = (captureId: string): Promise<void> =>
  api.delete(`/sandbox/messages/${encodeURIComponent(captureId)}`);

export type RawResult =
  | { ok: true; bytes: ArrayBuffer; contentType: string }
  | { ok: false; missing: true }
  | { ok: false; missing: false; error: unknown };

/** Fetch raw RFC822. A 404 means the body was not kept (e.g. system mail). */
export const getSandboxMessageRaw = async (captureId: string): Promise<RawResult> => {
  try {
    const res = await api.raw.get<ArrayBuffer>(
      `/sandbox/messages/${encodeURIComponent(captureId)}/raw`,
      { responseType: "arraybuffer" },
    );
    return { ok: true, bytes: res.data, contentType: "message/rfc822" };
  } catch (err) {
    if (err instanceof ApiProblemError && err.problem.status === 404) {
      return { ok: false, missing: true };
    }
    return { ok: false, missing: false, error: err };
  }
};

export const downloadSandboxRaw = async (captureId: string, filename: string): Promise<RawResult> => {
  const result = await getSandboxMessageRaw(captureId);
  if (!result.ok) return result;
  const blob = new Blob([result.bytes], { type: result.contentType });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename.endsWith(".eml") ? filename : `${filename}.eml`;
  a.click();
  URL.revokeObjectURL(url);
  return result;
};
