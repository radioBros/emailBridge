import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type ApiKey = components["schemas"]["Key"];
export type ApiKeyList = components["schemas"]["KeyList"];
export type ApiKeyCreateBody = components["schemas"]["CreateBody"];
export type ApiKeyCreated = components["schemas"]["CreatedBody"];
/** Patch may clear restrictions / rateLimit with null. */
export type ApiKeyPatchBody = Omit<
  components["schemas"]["PatchBody"],
  "restrictions" | "rateLimit"
> & {
  restrictions?: components["schemas"]["Restrictions"] | null;
  rateLimit?: components["schemas"]["RateLimit"] | null;
};

export interface PageQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  q?: string;
}

export type AccountView = components["schemas"]["AccountView"] & {
  ownerNode?: string;
};
export type AccountListBody = Omit<components["schemas"]["AccountListBody"], "data"> & {
  data: AccountView[] | null;
};

/** local until schema regen */
export interface EndpointListBody {
  data: Array<{ id: string; name: string; url?: string }> | null;
  total?: number;
}
/** local until schema regen */
export interface RouteListBody {
  data: Array<{ id: string; name: string }> | null;
  total?: number;
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

export const getAccount = (accountId: string): Promise<AccountView> =>
  api.get<AccountView>(`/accounts/${encodeURIComponent(accountId)}`);

export const searchAccounts = (q: string, limit = 20): Promise<AccountListBody> =>
  api.get<AccountListBody>(
    `/accounts${qs({ page: 1, limit, includeTotal: false, q: q || undefined })}`,
  );

export const searchEndpoints = (q: string, limit = 20): Promise<EndpointListBody> =>
  api.get<EndpointListBody>(
    `/webhook-endpoints${qs({ page: 1, limit, includeTotal: false, q: q || undefined })}`,
  );

export const searchRoutes = (q: string, limit = 20): Promise<RouteListBody> =>
  api.get<RouteListBody>(
    `/routes${qs({ page: 1, limit, includeTotal: false, q: q || undefined })}`,
  );
