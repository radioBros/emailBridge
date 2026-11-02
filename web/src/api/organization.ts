import { api } from "@/api/client";
import type { components } from "@/api/schema";

/** Membership plus live display fields (section C) until schema.d.ts regenerates. */
export type Membership = components["schemas"]["Membership"] & {
  name?: string;
  lastLoginAt?: string;
};
export type MembershipList = Omit<components["schemas"]["MembershipList"], "data"> & {
  data: Membership[] | null;
};
export type MemberCreateInBody = components["schemas"]["MemberCreateInBody"];
export type MemberPatchInBody = components["schemas"]["MemberPatchInBody"];

export type ApiKey = components["schemas"]["Key"];
export type ApiKeyList = components["schemas"]["KeyList"];
export type ApiKeyCreateBody = components["schemas"]["CreateBody"];
export type ApiKeyCreated = components["schemas"]["CreatedBody"];
export type ApiKeyPatchBody = components["schemas"]["PatchBody"];

export type AuditRecord = components["schemas"]["Record"];
export type AuditList = components["schemas"]["ListResult"];
export type AccountView = components["schemas"]["AccountView"] & {
  ownerNode?: string;
};
export type AccountListBody = Omit<components["schemas"]["AccountListBody"], "data"> & {
  data: AccountView[] | null;
};

export interface PageQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  q?: string;
}

export interface AuditQuery extends PageQuery {
  actorId?: string;
  action?: string;
  from?: string;
  to?: string;
  targetId?: string;
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

export const listMembers = (query: PageQuery = {}): Promise<MembershipList> =>
  api.get<MembershipList>(
    `/members${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort,
      q: query.q,
    })}`,
  );

export const inviteMember = (body: MemberCreateInBody): Promise<Membership> =>
  api.post<Membership>("/members", body);

export const patchMember = (
  memberId: string,
  body: MemberPatchInBody,
  version: number,
): Promise<Membership> =>
  api.raw
    .patch<Membership>(`/members/${encodeURIComponent(memberId)}`, body, {
      headers: { "If-Match": etag(version) },
    })
    .then((r) => r.data);

export const removeMember = (memberId: string, version: number): Promise<void> =>
  api.raw
    .delete(`/members/${encodeURIComponent(memberId)}`, {
      headers: { "If-Match": etag(version) },
    })
    .then(() => undefined);

export const listApiKeys = (query: PageQuery = {}): Promise<ApiKeyList> =>
  api.get<ApiKeyList>(
    `/api-keys${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort,
      q: query.q,
    })}`,
  );

export const getAccount = (accountId: string): Promise<AccountView> =>
  api.get<AccountView>(`/accounts/${encodeURIComponent(accountId)}`);

export const createApiKey = (body: ApiKeyCreateBody): Promise<ApiKeyCreated> =>
  api.post<ApiKeyCreated>("/api-keys", body);

export const patchApiKey = (
  keyId: string,
  body: ApiKeyPatchBody,
  version: number,
): Promise<ApiKey> =>
  api.raw
    .patch<ApiKey>(`/api-keys/${encodeURIComponent(keyId)}`, body, {
      headers: { "If-Match": etag(version) },
    })
    .then((r) => r.data);

export const revokeApiKey = (keyId: string, version: number): Promise<void> =>
  api.raw
    .delete(`/api-keys/${encodeURIComponent(keyId)}`, {
      headers: { "If-Match": etag(version) },
    })
    .then(() => undefined);

export const listAuditLog = (query: AuditQuery = {}): Promise<AuditList> =>
  api.get<AuditList>(
    `/audit-log${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "-at",
      actorId: query.actorId,
      action: query.action,
      from: query.from,
      to: query.to,
      targetId: query.targetId,
    })}`,
  );

/** Account picker for API-key binding (server search). */
export const searchAccounts = (q: string, limit = 20): Promise<AccountListBody> =>
  api.get<AccountListBody>(
    `/accounts${qs({ page: 1, limit, includeTotal: false, q: q || undefined })}`,
  );
