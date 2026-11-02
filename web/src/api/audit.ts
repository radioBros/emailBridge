import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type AuditRecord = components["schemas"]["Record"];
export type AuditList = components["schemas"]["ListResult"];

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
  /** Resource / target id filter (S2 targetId). */
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

/** CSV export is not in the API yet (P3.4a note). */
export const auditCsvExportAvailable = false;
