import { api } from "@/api/client";

// local until schema regen (P5.1)

export type TemplateView = {
  id: string;
  organizationId: string;
  name: string;
  engine: string;
  subject?: string;
  text?: string;
  html?: string;
  partials?: Record<string, string>;
  strict?: boolean;
  locale?: string;
  timezone?: string;
  currentVersion: number;
  createdBy?: string;
  createdAt: string;
  updatedAt: string;
  version: number;
};

export type TemplateCreate = {
  name: string;
  subject?: string;
  text?: string;
  html?: string;
  partials?: Record<string, string>;
  strict?: boolean;
  locale?: string;
  timezone?: string;
};

export type TemplateUpdate = {
  name?: string;
  subject?: string;
  text?: string;
  html?: string;
  partials?: Record<string, string>;
  strict?: boolean;
  locale?: string;
  timezone?: string;
};

export type TemplateList = {
  data: TemplateView[] | null;
  nextCursor?: string | null;
  total?: number | null;
  totalCapped?: boolean;
};

export type TemplatePreviewInput = {
  version?: number;
  variables?: Record<string, unknown>;
  locale?: string;
  timezone?: string;
};

export type TemplatePreviewResult = {
  subject?: string;
  text?: string;
  html?: string;
};

export interface TemplateListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  cursor?: string;
  q?: string;
}

const qs = (params: TemplateListParams): string => {
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

export const listTemplates = (params: TemplateListParams = {}): Promise<TemplateList> =>
  api.get<TemplateList>(`/templates${qs({ includeTotal: true, ...params })}`);

export const getTemplate = (id: string): Promise<TemplateView> =>
  api.get<TemplateView>(`/templates/${encodeURIComponent(id)}`);

export const createTemplate = (body: TemplateCreate): Promise<TemplateView> =>
  api.post<TemplateView>("/templates", body);

export const patchTemplate = (id: string, body: TemplateUpdate): Promise<TemplateView> =>
  api.patch<TemplateView>(`/templates/${encodeURIComponent(id)}`, body);

export const deleteTemplate = (id: string): Promise<void> =>
  api.delete(`/templates/${encodeURIComponent(id)}`);

export const previewTemplate = (
  id: string,
  body: TemplatePreviewInput,
): Promise<TemplatePreviewResult> =>
  api.post<TemplatePreviewResult>(`/templates/${encodeURIComponent(id)}/preview`, body);
