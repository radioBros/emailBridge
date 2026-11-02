import { api } from "@/api/client";

/** local until schema regen — P8.1 integrations */

export type IntegrationCategory =
  | "chat"
  | "helpdesk"
  | "automation"
  | "sending"
  | "spam"
  | "storage"
  | "crm"
  | "sso"
  | "observability";

export interface IntegrationCatalogueItem {
  id: string;
  version: string;
  name: string;
  category: IntegrationCategory | string;
  icon?: string;
  color?: string;
  description?: string;
  docsPath?: string;
  configSchema: Record<string, unknown>;
  secretFields: string[];
  capabilities: string[];
  installedCount: number;
  instanceIds?: string[];
  runtimeHint?: string;
}

export interface IntegrationCatalogue {
  data: IntegrationCatalogueItem[] | null;
}

export interface CreatedResources {
  endpoints?: string[];
  routes?: string[];
  settings?: string[];
  gateways?: string[];
}

export interface IntegrationInstance {
  id: string;
  organizationId: string;
  type: string;
  definitionVersion: string;
  name: string;
  config: Record<string, unknown>;
  secretsSet: Record<string, boolean>;
  createdResources: CreatedResources;
  enabled: boolean;
  createdAt: string;
  updatedAt: string;
  version: number;
  lastActionAt?: string;
  lastActionOK?: boolean;
  lastActionError?: string;
  lastActionDetail?: string;
  gatewayId?: string;
}

export interface IntegrationInstanceList {
  data: IntegrationInstance[] | null;
  nextCursor: string | null;
  total?: number;
  totalCapped?: boolean;
}

export interface IntegrationInstall {
  name?: string;
  config: Record<string, unknown>;
  secrets?: Record<string, string>;
  runTest?: boolean;
}

export interface IntegrationInstancePatch {
  name?: string;
  config?: Record<string, unknown>;
  secrets?: Record<string, string>;
  enabled?: boolean;
}

export interface IntegrationTestResult {
  ok: boolean;
  latencyMs: number;
  detail?: string;
}

export interface PageQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  q?: string;
  type?: string;
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

export const listIntegrations = (params?: {
  q?: string;
  category?: string;
}): Promise<IntegrationCatalogue> =>
  api.get(`/integrations${qs({ q: params?.q, category: params?.category })}`);

export const installIntegration = (
  type: string,
  body: IntegrationInstall,
): Promise<IntegrationInstance> =>
  api.post(`/integrations/${encodeURIComponent(type)}/install`, body);

export const listIntegrationInstances = (
  params?: PageQuery,
): Promise<IntegrationInstanceList> =>
  api.get(
    `/integration-instances${qs({
      page: params?.page,
      limit: params?.limit,
      includeTotal: params?.includeTotal,
      sort: params?.sort,
      q: params?.q,
      type: params?.type,
    })}`,
  );

export const getIntegrationInstance = (id: string): Promise<IntegrationInstance> =>
  api.get(`/integration-instances/${encodeURIComponent(id)}`);

export const patchIntegrationInstance = (
  id: string,
  body: IntegrationInstancePatch,
  version: number,
): Promise<IntegrationInstance> =>
  api.raw
    .patch<IntegrationInstance>(`/integration-instances/${encodeURIComponent(id)}`, body, {
      headers: { "If-Match": etag(version) },
    })
    .then((r) => r.data);

export const deleteIntegrationInstance = (id: string, version: number): Promise<void> =>
  api.raw
    .delete(`/integration-instances/${encodeURIComponent(id)}`, {
      headers: { "If-Match": etag(version) },
    })
    .then(() => undefined);

export const testIntegrationInstance = (id: string): Promise<IntegrationTestResult> =>
  api.post(`/integration-instances/${encodeURIComponent(id)}/test`, {});
