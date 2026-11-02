import { api } from "@/api/client";
import type { components } from "@/api/schema";

/** Pending OpenAPI regen (brief 30c) — local until Claude regenerates schema.d.ts. */
export type DeliveryOptions = {
  ordering: "strict" | "bestEffort";
  maxConcurrency: number;
  contentType: "json" | "cloudevents";
  retryOn4xx: boolean;
  autoDisableAfter?: string;
};

export type DeliveryUpdate = {
  ordering?: "strict" | "bestEffort";
  maxConcurrency?: number;
  contentType?: "json" | "cloudevents";
  retryOn4xx?: boolean;
  autoDisableAfter?: string;
};

export type DisabledReason = {
  code: string;
  at: string;
};

export type EndpointTLSWrite = components["schemas"]["TLSInput"];
/** Write shape for endpoint security (schema: SecurityWrite / WebhookSecurityWrite). */
export type SecurityOptions = components["schemas"]["SecurityWrite"];
export type EndpointView = components["schemas"]["EndpointView"] & {
  delivery?: DeliveryOptions;
  disabledReason?: DisabledReason | null;
  security?: SecurityOptions;
};
export type EndpointCreate = Omit<components["schemas"]["EndpointCreate"], "security"> & {
  delivery?: DeliveryOptions;
  security?: SecurityOptions;
};
export type EndpointUpdate = Omit<components["schemas"]["EndpointUpdate"], "security"> & {
  delivery?: DeliveryUpdate;
  security?: SecurityOptions;
};
export type EndpointList = components["schemas"]["EndpointList"];
export type RotateSecretResult = components["schemas"]["RotateSecretResult"];
export type DeliveryAttempt = components["schemas"]["DeliveryAttempt"];
export type WebhookHeader = components["schemas"]["WebhookHeader"];
export type PayloadOptions = components["schemas"]["PayloadOptions"];
export type RetryOptions = components["schemas"]["RetryOptions"];

/** Pending OpenAPI field (brief 19b / follow-up common §C). */
export type EndpointLastDelivery = {
  state: string;
  statusCode?: number;
  at: string;
};

export type EndpointViewRow = EndpointView & {
  lastDelivery?: EndpointLastDelivery | null;
};

export type EndpointListRow = Omit<EndpointList, "data"> & {
  data: EndpointViewRow[] | null;
};

export interface WebhookListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  cursor?: string;
  q?: string;
}

const qs = (params: WebhookListParams): string => {
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

export const listWebhookEndpoints = (params: WebhookListParams = {}): Promise<EndpointListRow> =>
  api.get<EndpointListRow>(`/webhook-endpoints${qs({ includeTotal: true, ...params })}`);

export const getWebhookEndpoint = (id: string): Promise<EndpointView> =>
  api.get<EndpointView>(`/webhook-endpoints/${encodeURIComponent(id)}`);

export const createWebhookEndpoint = (body: EndpointCreate): Promise<EndpointView> =>
  api.post<EndpointView>("/webhook-endpoints", body);

export const patchWebhookEndpoint = (id: string, body: EndpointUpdate): Promise<EndpointView> =>
  api.patch<EndpointView>(`/webhook-endpoints/${encodeURIComponent(id)}`, body);

export const deleteWebhookEndpoint = (id: string): Promise<void> =>
  api.delete(`/webhook-endpoints/${encodeURIComponent(id)}`);

export const rotateWebhookEndpointSecret = (id: string): Promise<RotateSecretResult> =>
  api.post<RotateSecretResult>(`/webhook-endpoints/${encodeURIComponent(id)}/rotate-secret`);

export const testWebhookEndpoint = (id: string): Promise<DeliveryAttempt> =>
  api.post<DeliveryAttempt>(`/webhook-endpoints/${encodeURIComponent(id)}/test`);
