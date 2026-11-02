import { api } from "@/api/client";

/** local until schema regen — P6.1 oauth apps */
export type OauthProvider = "gmail" | "outlook" | "mailru" | "generic";

export interface OauthApp {
  id: string;
  organizationId: string;
  name: string;
  provider: OauthProvider;
  clientId: string;
  hasClientSecret: boolean;
  hasServiceKey?: boolean;
  tenantId?: string;
  appOnly?: boolean;
  authorizeUrl?: string;
  tokenUrl?: string;
  revokeUrl?: string;
  scopes?: string[];
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
  version: number;
}

export interface OauthAppList {
  data: OauthApp[] | null;
  nextCursor: string | null;
  total?: number;
  totalCapped?: boolean;
}

export interface OauthAppCreate {
  name: string;
  provider: OauthProvider;
  clientId: string;
  clientSecret: string;
  serviceKey?: string;
  tenantId?: string;
  appOnly?: boolean;
  authorizeUrl?: string;
  tokenUrl?: string;
  revokeUrl?: string;
  scopes?: string[];
  enabled?: boolean;
}

export interface OauthAppPatch {
  name?: string;
  clientId?: string;
  clientSecret?: string;
  serviceKey?: string;
  tenantId?: string | null;
  appOnly?: boolean;
  authorizeUrl?: string | null;
  tokenUrl?: string | null;
  revokeUrl?: string | null;
  scopes?: string[];
  enabled?: boolean;
}

export interface OauthVerifyResult {
  ok: boolean;
  latencyMs: number;
  authorizeReachable?: boolean;
  tokenReachable?: boolean;
  code?: string;
  detail?: string;
}

export interface OauthStartResponse {
  authorizeUrl: string;
  state: string;
}

export interface PageQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
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

const etag = (version: number): string => `"v${version}"`;

export const listOauthApps = (query: PageQuery = {}): Promise<OauthAppList> =>
  api.get<OauthAppList>(
    `/oauth-apps${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort,
      q: query.q,
    })}`,
  );

export const createOauthApp = (body: OauthAppCreate): Promise<OauthApp> =>
  api.post<OauthApp>("/oauth-apps", body);

export const getOauthApp = (id: string): Promise<OauthApp> =>
  api.get<OauthApp>(`/oauth-apps/${encodeURIComponent(id)}`);

export const patchOauthApp = (
  id: string,
  body: OauthAppPatch,
  version: number,
): Promise<OauthApp> =>
  api.raw
    .patch<OauthApp>(`/oauth-apps/${encodeURIComponent(id)}`, body, {
      headers: { "If-Match": etag(version) },
    })
    .then((r) => r.data);

export const deleteOauthApp = (id: string, version: number): Promise<void> =>
  api.raw
    .delete(`/oauth-apps/${encodeURIComponent(id)}`, {
      headers: { "If-Match": etag(version) },
    })
    .then(() => undefined);

export const verifyOauthApp = (id: string): Promise<OauthVerifyResult> =>
  api.post<OauthVerifyResult>(`/oauth-apps/${encodeURIComponent(id)}/verify`, {});

export const startAccountOauth = (
  accountId: string,
  appId: string,
): Promise<OauthStartResponse> =>
  api.post<OauthStartResponse>(
    `/accounts/${encodeURIComponent(accountId)}/oauth/start`,
    { appId },
  );
