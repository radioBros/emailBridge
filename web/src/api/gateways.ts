import { api } from "@/api/client";

// local until schema regen (P5.3 / W36)

export type GatewayTls = "implicit" | "starttls" | "none";
export type GatewayPolicy = "standard" | "atMostOnce";
export type GatewayKind = "smtp" | "ses" | "postmark" | "mailgun" | "sendgrid" | "brevo";

export type GatewayAuthView = {
  user: string;
  hasPassword: boolean;
};

export type GatewayAPIConfig = {
  region?: string;
  domain?: string;
  baseUrl?: string;
  from?: string;
  extra?: Record<string, string>;
};

export type GatewayCredentialsFlags = {
  hasAccessKey?: boolean;
  hasSecretKey?: boolean;
  hasApiKey?: boolean;
  hasWebhookSecret?: boolean;
};

export type GatewayCredentialsInput = {
  accessKey?: string;
  secretKey?: string;
  apiKey?: string;
  webhookSecret?: string;
};

export type GatewayView = {
  id: string;
  organizationId: string;
  name: string;
  kind: GatewayKind | string;
  host?: string;
  port?: number;
  tls?: GatewayTls | string;
  auth?: GatewayAuthView;
  api?: GatewayAPIConfig;
  credentials?: GatewayCredentialsFlags;
  webhookUrl?: string;
  ehloName?: string;
  localAddress?: string;
  proxy?: string;
  policy?: GatewayPolicy | string;
  maxAttempts?: number | null;
  backoffBase?: string;
  ignoreCertErrors?: boolean;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
  version: number;
};

export type GatewayAuthInput = {
  user: string;
  pass?: string;
};

export type GatewayCreate = {
  name: string;
  kind?: GatewayKind | string;
  host?: string;
  port?: number;
  tls?: GatewayTls | string;
  auth?: GatewayAuthInput;
  api?: GatewayAPIConfig;
  credentials?: GatewayCredentialsInput;
  ehloName?: string;
  localAddress?: string;
  proxy?: string;
  policy?: GatewayPolicy | string;
  maxAttempts?: number;
  backoffBase?: string;
  ignoreCertErrors?: boolean;
  enabled?: boolean;
};

export type GatewayUpdate = {
  name?: string;
  kind?: GatewayKind | string;
  host?: string;
  port?: number;
  tls?: GatewayTls | string;
  auth?: GatewayAuthInput;
  api?: GatewayAPIConfig;
  credentials?: GatewayCredentialsInput;
  ehloName?: string;
  localAddress?: string;
  proxy?: string;
  policy?: GatewayPolicy | string;
  maxAttempts?: number | null;
  backoffBase?: string;
  ignoreCertErrors?: boolean;
  enabled?: boolean;
};

export type GatewayList = {
  data: GatewayView[] | null;
  nextCursor?: string | null;
  total?: number | null;
  totalCapped?: boolean;
};

export type GatewayVerifyResult = {
  ok: boolean;
  latencyMs: number;
  capabilities?: string[];
  code?: string;
  detail?: string;
};

export interface GatewayListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  cursor?: string;
  q?: string;
}

const qs = (params: GatewayListParams): string => {
  const sp = new URLSearchParams();
  if (params.page != null) sp.set("page", String(params.page));
  if (params.limit != null) sp.set("limit", String(params.limit));
  if (params.includeTotal != null) sp.set("includeTotal", String(params.includeTotal));
  if (params.sort) sp.set("sort", params.sort);
  if (params.cursor) sp.set("cursor", params.cursor);
  if (params.q) sp.set("q", params.q);
  const s = sp.toString();
  return s ? `?${s}` : "";
};

export const listSmtpGateways = (params: GatewayListParams = {}): Promise<GatewayList> =>
  api.get<GatewayList>(`/smtp-gateways${qs({ includeTotal: true, ...params })}`);

export const getSmtpGateway = (id: string): Promise<GatewayView> =>
  api.get<GatewayView>(`/smtp-gateways/${encodeURIComponent(id)}`);

export const createSmtpGateway = (body: GatewayCreate): Promise<GatewayView> =>
  api.post<GatewayView>("/smtp-gateways", body);

export const patchSmtpGateway = (id: string, body: GatewayUpdate): Promise<GatewayView> =>
  api.patch<GatewayView>(`/smtp-gateways/${encodeURIComponent(id)}`, body);

export const deleteSmtpGateway = (id: string): Promise<void> =>
  api.delete(`/smtp-gateways/${encodeURIComponent(id)}`);

export const verifySmtpGateway = (id: string): Promise<GatewayVerifyResult> =>
  api.post<GatewayVerifyResult>(`/smtp-gateways/${encodeURIComponent(id)}/verify`);
