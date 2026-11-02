import { api } from "@/api/client";
import type { components } from "@/api/schema";

/** Until schema regen includes `ownerNode` (follow-up 18b §C). */
export type AccountView = components["schemas"]["AccountView"] & {
  ownerNode?: string;
};
export type AccountListBody = Omit<components["schemas"]["AccountListBody"], "data"> & {
  data: AccountView[] | null;
};
export type CreateInput = components["schemas"]["CreateInput"];
export type AccountsPatchBody = components["schemas"]["AccountsPatchBody"];
export type VerifyRequest = components["schemas"]["VerifyRequest"];
export type VerifyResult = components["schemas"]["VerifyResult"];
export type VerifyProtocolResult = components["schemas"]["VerifyProtocolResult"];
export type SyncInput = components["schemas"]["SyncInput"];
export type JobAccepted = components["schemas"]["JobAccepted"];
export type ProtocolLogRecord = components["schemas"]["ProtocolLogRecord"];
export type ProtocolLogRecordList = components["schemas"]["ProtocolLogRecordList"];
export type SubmitView = components["schemas"]["SubmitView"];
export type SubmitList = components["schemas"]["SubmitList"];
export type IMAPConfigInput = components["schemas"]["IMAPConfigInput"];
export type SMTPConfigInput = components["schemas"]["SMTPConfigInput"];
export type AccountsSyncConfig = components["schemas"]["AccountsSyncConfig"];

export interface AccountListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  state?: string;
  q?: string;
  cursor?: string;
}

export interface ProtocolLogQuery {
  limit?: number;
  cursor?: string;
  from?: string;
  to?: string;
  proto?: "imap" | "smtp";
  connId?: string;
}

export interface OutboundListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  accountId?: string;
  state?: string;
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

const etag = (version: number): string => `"v${version}"`;

export const listAccounts = (query: AccountListQuery = {}): Promise<AccountListBody> =>
  api.get<AccountListBody>(
    `/accounts${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "name",
      state: query.state,
      q: query.q,
      cursor: query.cursor,
    })}`,
  );

export const getAccount = (accountId: string): Promise<AccountView> =>
  api.get<AccountView>(`/accounts/${encodeURIComponent(accountId)}`);

export const createAccount = (body: CreateInput): Promise<AccountView> =>
  api.post<AccountView>("/accounts", body);

export const patchAccount = (
  accountId: string,
  body: AccountsPatchBody,
  version?: number,
): Promise<AccountView> => {
  const headers: Record<string, string> = {};
  if (version != null) headers["If-Match"] = etag(version);
  return api.raw
    .patch<AccountView>(`/accounts/${encodeURIComponent(accountId)}`, body, { headers })
    .then((r) => r.data);
};

export const deleteAccount = (accountId: string, version?: number): Promise<JobAccepted> => {
  const headers: Record<string, string> = {};
  if (version != null) headers["If-Match"] = etag(version);
  return api.raw
    .delete<JobAccepted>(`/accounts/${encodeURIComponent(accountId)}`, { headers })
    .then((r) => r.data);
};

export const verifyAccountSettings = (body: VerifyRequest): Promise<VerifyResult> =>
  api.post<VerifyResult>("/accounts/verify", body);

export const syncAccount = (accountId: string, body: SyncInput): Promise<JobAccepted> =>
  api.post<JobAccepted>(`/accounts/${encodeURIComponent(accountId)}/sync`, body);

export const reconnectAccount = (accountId: string): Promise<JobAccepted> =>
  api.post<JobAccepted>(`/accounts/${encodeURIComponent(accountId)}/reconnect`);

export const resumeAccount = (accountId: string): Promise<AccountView> =>
  api.post<AccountView>(`/accounts/${encodeURIComponent(accountId)}/resume`);

export const listProtocolLogs = (
  accountId: string,
  query: ProtocolLogQuery = {},
): Promise<ProtocolLogRecordList> =>
  api.get<ProtocolLogRecordList>(
    `/accounts/${encodeURIComponent(accountId)}/protocol-logs${qs({
      limit: query.limit ?? 100,
      cursor: query.cursor,
      from: query.from,
      to: query.to,
      proto: query.proto,
      connId: query.connId,
    })}`,
  );

/** Download protocol logs as NDJSON; returns the raw text body. */
export const downloadProtocolLogs = async (
  accountId: string,
  query: Omit<ProtocolLogQuery, "limit" | "cursor"> = {},
): Promise<Blob> => {
  const res = await api.raw.get<Blob>(
    `/accounts/${encodeURIComponent(accountId)}/protocol-logs/download${qs({
      from: query.from,
      to: query.to,
      proto: query.proto,
      connId: query.connId,
    })}`,
    { responseType: "blob" },
  );
  return res.data;
};

/** Outbox rows for one account (`GET /v1/outbound-messages?accountId=`). */
export const listAccountOutbound = (
  accountId: string,
  query: Omit<OutboundListQuery, "accountId"> = {},
): Promise<SubmitList> =>
  api.get<SubmitList>(
    `/outbound-messages${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "-createdAt",
      accountId,
      state: query.state,
    })}`,
  );
