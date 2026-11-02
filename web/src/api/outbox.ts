import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type SubmitView = components["schemas"]["SubmitView"];
export type SubmitList = components["schemas"]["SubmitList"];
export type AccountView = components["schemas"]["AccountView"];
export type AccountListBody = components["schemas"]["AccountListBody"];

export interface OutboundListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  accountId?: string;
  state?: string;
  messageId?: string;
  /** Envelope sender address (not a date). */
  from?: string;
  /** Envelope recipient address (not a date). */
  to?: string;
  /** Created at or after (RFC 3339). */
  createdFrom?: string;
  /** Created before (RFC 3339, exclusive). */
  createdTo?: string;
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

export const listOutboundMessages = (query: OutboundListQuery = {}): Promise<SubmitList> =>
  api.get<SubmitList>(
    `/outbound-messages${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "-createdAt",
      accountId: query.accountId,
      state: query.state,
      messageId: query.messageId,
      from: query.from,
      to: query.to,
      createdFrom: query.createdFrom,
      createdTo: query.createdTo,
    })}`,
  );

export const getOutboundMessage = (outboundId: string): Promise<SubmitView> =>
  api.get<SubmitView>(`/outbound-messages/${encodeURIComponent(outboundId)}`);

export const cancelOutboundMessage = (
  outboundId: string,
  version?: number,
): Promise<SubmitView> => {
  const headers: Record<string, string> = {};
  if (version != null) headers["If-Match"] = `"v${version}"`;
  return api.raw
    .delete<SubmitView>(`/outbound-messages/${encodeURIComponent(outboundId)}`, { headers })
    .then((r) => r.data);
};

/** Account picker for filters (server search over `/v1/accounts`). */
export const searchAccounts = async (q = ""): Promise<AccountView[]> => {
  const res = await api.get<AccountListBody>(
    `/accounts${qs({ page: 1, limit: 50, includeTotal: false, q: q || undefined, sort: "name" })}`,
  );
  return res.data ?? [];
};
