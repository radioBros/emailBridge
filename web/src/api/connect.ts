import { api, ApiProblemError } from "@/api/client";
import type { components } from "@/api/schema";

export type BrandingPalette = components["schemas"]["BrandingPalette"];
export type ConnectBranding = components["schemas"]["Branding"];
/** Asset ids accept null to clear; generated schema omits nullability. */
export type BrandingUpdate = Omit<
  components["schemas"]["BrandingUpdate"],
  "logoAssetId" | "logoDarkAssetId" | "faviconAssetId"
> & {
  logoAssetId?: string | null;
  logoDarkAssetId?: string | null;
  faviconAssetId?: string | null;
};
export type BrandingPreview = components["schemas"]["BrandingPreview"];
export type BrandingAsset = components["schemas"]["BrandingAsset"] & {
  url: string;
  kind: BrandingAssetKind;
};
export type BrandingAssetKind = "logo" | "logoDark" | "favicon" | "image";

export interface ConnectOauthApp {
  id: string;
  name: string;
  provider: string;
}

export interface ConnectBootstrap {
  branding: ConnectBranding;
  mode: "hosted" | "oauth";
  oauthApps?: ConnectOauthApp[];
  csrf: string;
  expiresAt: string;
}

export interface AutoconfigServer {
  host: string;
  port: number;
  tls: "implicit" | "starttls" | "none";
  user?: string;
}

export interface AutoconfigResult {
  email: string;
  domain: string;
  source: string;
  imap?: AutoconfigServer;
  smtp?: AutoconfigServer;
  oauthHint?: "gmail" | "outlook" | null;
}

export interface ConnectDone {
  accountId: string;
  redirectUrl?: string;
}

const enc = (token: string): string => encodeURIComponent(token);

export const bootstrapConnect = (token: string): Promise<ConnectBootstrap> =>
  api.get<ConnectBootstrap>(`/public/connect/${enc(token)}`);

export const discoverConnect = (
  token: string,
  body: { email: string; csrf: string },
): Promise<AutoconfigResult> =>
  api.post<AutoconfigResult>(`/public/connect/${enc(token)}/discover`, body);

export const passwordConnect = (
  token: string,
  body: {
    email: string;
    password: string;
    name?: string;
    csrf: string;
    imap?: AutoconfigServer;
    smtp?: AutoconfigServer;
  },
): Promise<ConnectDone> =>
  api.post<ConnectDone>(`/public/connect/${enc(token)}/password`, body);

export const oauthStartConnect = (
  token: string,
  body: { email: string; appId: string; csrf: string },
): Promise<{ authorizeUrl: string }> =>
  api.post<{ authorizeUrl: string }>(`/public/connect/${enc(token)}/oauth/start`, body);

/** P6.1 oauth-bound link redeem (302 to IdP). Path is outside baseURL /v1 → use absolute. */
export const redeemConnectLinkURL = (token: string): string =>
  `/v1/connect-links/${enc(token)}/redeem`;

const etagOf = (headers: Headers | Record<string, string> | undefined): string | undefined => {
  if (!headers) return undefined;
  if (headers instanceof Headers) return headers.get("ETag") ?? headers.get("etag") ?? undefined;
  return headers.ETag ?? headers.etag ?? undefined;
};

export const getConnectBranding = (): Promise<{ data: ConnectBranding; etag?: string }> =>
  api.raw.get<ConnectBranding>("/connect/branding").then((r) => ({
    data: r.data,
    etag: etagOf(r.headers),
  }));

export const patchConnectBranding = (
  body: BrandingUpdate,
  etag?: string,
): Promise<{ data: ConnectBranding; etag?: string }> => {
  const headers: Record<string, string> = {};
  if (etag) headers["If-Match"] = etag;
  return api.raw
    .patch<ConnectBranding>("/connect/branding", body, { headers })
    .then((r) => ({
      data: r.data,
      etag: etagOf(r.headers),
    }));
};

export const previewConnectBranding = (body: {
  headerHtml?: string;
  headHtml?: string;
}): Promise<BrandingPreview> =>
  api.post<BrandingPreview>("/connect/branding/preview", body);

export const listBrandingAssets = (
  kind?: BrandingAssetKind,
): Promise<BrandingAsset[]> => {
  const q = kind ? `?kind=${enc(kind)}` : "";
  return api.get<{ data: BrandingAsset[] }>(`/connect/branding/assets${q}`).then((r) => r.data ?? []);
};

export const uploadBrandingAsset = (
  kind: BrandingAssetKind,
  file: File,
): Promise<BrandingAsset> => {
  const form = new FormData();
  form.append("file", file);
  form.append("kind", kind);
  // Let the browser set multipart boundary — do not force Content-Type.
  return api.raw.post<BrandingAsset>("/connect/branding/assets", form).then((r) => r.data);
};

export const deleteBrandingAsset = (id: string): Promise<void> =>
  api.delete(`/connect/branding/assets/${enc(id)}`);

export const publicBrandingAssetPath = (id: string): string =>
  `/v1/public/branding/assets/${enc(id)}`;

export const connectErrorCode = (err: unknown): string => {
  if (err instanceof ApiProblemError) return err.problem.code;
  return "internal_error";
};
