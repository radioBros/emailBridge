import { api } from "@/api/client";

/** local until schema regen */
export interface OidcPublicProvider {
  id: string;
  buttonLabel: string;
}

/** local until schema regen */
export interface OidcProvider {
  id: string;
  issuerUrl: string;
  clientId: string;
  scopes: string[];
  buttonLabel: string;
  allowedEmailDomains?: string[];
  groupClaim?: string;
  groupRoles?: Record<string, string>;
  autoProvision: boolean;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
}

/** local until schema regen */
export interface OidcProviderCreate {
  issuerUrl: string;
  clientId: string;
  clientSecret: string;
  scopes?: string[];
  buttonLabel: string;
  allowedEmailDomains?: string[];
  groupClaim?: string;
  groupRoles?: Record<string, string>;
  autoProvision?: boolean;
  enabled?: boolean;
}

/** local until schema regen */
export type OidcProviderPatch = Partial<Omit<OidcProviderCreate, "clientSecret">> & {
  clientSecret?: string;
};

export const listPublicOidcProviders = async (): Promise<OidcPublicProvider[]> => {
  const body = await api.get<{ data: OidcPublicProvider[] }>("/auth/oidc/providers");
  return body.data ?? [];
};

export const oidcStartUrl = (providerId: string, redirectUri?: string): string => {
  const q = new URLSearchParams();
  if (redirectUri) q.set("redirect_uri", redirectUri);
  const qs = q.toString();
  return `/v1/auth/oidc/${encodeURIComponent(providerId)}/start${qs ? `?${qs}` : ""}`;
};

export const listOidcProviders = async (): Promise<OidcProvider[]> => {
  const body = await api.get<{ data: OidcProvider[] }>("/oidc-providers");
  return body.data ?? [];
};

export const createOidcProvider = async (body: OidcProviderCreate): Promise<OidcProvider> =>
  api.post<OidcProvider>("/oidc-providers", body);

export const patchOidcProvider = async (
  id: string,
  body: OidcProviderPatch,
): Promise<OidcProvider> =>
  api.patch<OidcProvider>(`/oidc-providers/${encodeURIComponent(id)}`, body);

export const deleteOidcProvider = async (id: string): Promise<void> => {
  await api.delete(`/oidc-providers/${encodeURIComponent(id)}`);
};
