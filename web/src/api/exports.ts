import { api } from "@/api/client";

// local until schema regen (P7.2)

export type ExportState =
  | "queued"
  | "running"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "expired";

export type ExportInclude = {
  raw?: boolean;
  attachments?: boolean;
  metadataOnly?: boolean;
};

export type ExportFilters = {
  folderIds?: string[];
  since?: string;
  before?: string;
  include: ExportInclude;
};

export type ExportProgress = {
  done: number;
  total: number;
  checkpoint?: { mailboxId: string; uid: number };
  nextPart?: number;
  partCount?: number;
};

export type ExportView = {
  id: string;
  organizationId: string;
  accountId: string;
  state: ExportState | string;
  jobId?: string;
  filters: ExportFilters;
  progress?: ExportProgress;
  messageCount?: number;
  byteSize?: number;
  error?: { code?: string; detail?: string; title?: string };
  createdBy?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  expireAt?: string;
};

export type ExportListBody = {
  items: ExportView[];
  nextCursor?: string;
  total?: number | null;
  totalIsEstimate?: boolean;
};

export type ExportCreateInput = {
  folderIds?: string[];
  since?: string;
  before?: string;
  include: ExportInclude;
};

export type ExportAccepted = {
  export: ExportView;
  job?: unknown;
};

export type SignedExportURL = {
  url: string;
  expiresAt: string;
};

export interface ExportListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  cursor?: string;
}

const acc = (accountId: string): string =>
  `/accounts/${encodeURIComponent(accountId)}/exports`;

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

export const listExports = (
  accountId: string,
  params: ExportListParams = {},
): Promise<ExportListBody> =>
  api.get<ExportListBody>(
    `${acc(accountId)}${qs({
      page: params.page,
      limit: params.limit,
      includeTotal: params.includeTotal,
      sort: params.sort,
      cursor: params.cursor,
    })}`,
  );

export const getExport = (accountId: string, exportId: string): Promise<ExportView> =>
  api.get<ExportView>(`${acc(accountId)}/${encodeURIComponent(exportId)}`);

export const createExport = (
  accountId: string,
  body: ExportCreateInput,
): Promise<ExportAccepted> => api.post<ExportAccepted>(acc(accountId), body);

export const cancelExport = (accountId: string, exportId: string): Promise<ExportView> =>
  api.post<ExportView>(`${acc(accountId)}/${encodeURIComponent(exportId)}/cancel`);

/** Authenticated download URL (browser navigates / fetches with cookies). */
export const exportDownloadPath = (accountId: string, exportId: string): string =>
  `/v1${acc(accountId)}/${encodeURIComponent(exportId)}/download`;

export const signedExportURL = (
  accountId: string,
  exportId: string,
): Promise<SignedExportURL> =>
  api.get<SignedExportURL>(
    `${acc(accountId)}/${encodeURIComponent(exportId)}/download?url=signed`,
  );
