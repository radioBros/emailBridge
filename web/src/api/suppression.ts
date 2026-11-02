import { api } from "@/api/client";

// local until schema regen (P5.2) — suppression package may still evolve

export type SuppressionKind = "manual" | "unsubscribe" | "bounce" | "complaint";
export type SuppressionSource =
  | "manual"
  | "unsubscribe"
  | "bounce"
  | "complaint"
  | "import";

export type SuppressionListView = {
  id: string;
  organizationId: string;
  name: string;
  kind: SuppressionKind | string;
  description?: string;
  createdAt: string;
  updatedAt: string;
  version: number;
};

export type SuppressionEntryView = {
  id: string;
  listId: string;
  organizationId: string;
  address: string;
  reason?: string;
  source: SuppressionSource | string;
  at: string;
};

export type SuppressionListCreate = {
  name: string;
  kind?: SuppressionKind | string;
  description?: string;
};

export type SuppressionListUpdate = {
  name?: string;
  description?: string;
};

export type SuppressionEntryCreate = {
  address: string;
  reason?: string;
};

export type SuppressionListPage = {
  data: SuppressionListView[] | null;
  nextCursor?: string | null;
  total?: number | null;
  totalCapped?: boolean;
};

export type SuppressionEntryPage = {
  data: SuppressionEntryView[] | null;
  nextCursor?: string | null;
  total?: number | null;
  totalCapped?: boolean;
};

export type SuppressionImportResult = {
  imported: number;
  skipped: number;
  invalid: number;
};

export interface SuppressionListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  cursor?: string;
  q?: string;
  kind?: string;
}

export interface SuppressionEntryParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  cursor?: string;
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

export const listSuppressionLists = (
  params: SuppressionListParams = {},
): Promise<SuppressionListPage> =>
  api.get<SuppressionListPage>(
    `/suppression-lists${qs({ includeTotal: true, ...params })}`,
  );

export const getSuppressionList = (id: string): Promise<SuppressionListView> =>
  api.get<SuppressionListView>(`/suppression-lists/${encodeURIComponent(id)}`);

export const createSuppressionList = (
  body: SuppressionListCreate,
): Promise<SuppressionListView> => api.post<SuppressionListView>("/suppression-lists", body);

export const patchSuppressionList = (
  id: string,
  body: SuppressionListUpdate,
): Promise<SuppressionListView> =>
  api.patch<SuppressionListView>(`/suppression-lists/${encodeURIComponent(id)}`, body);

export const deleteSuppressionList = (id: string): Promise<void> =>
  api.delete(`/suppression-lists/${encodeURIComponent(id)}`);

export const listSuppressionEntries = (
  listId: string,
  params: SuppressionEntryParams = {},
): Promise<SuppressionEntryPage> =>
  api.get<SuppressionEntryPage>(
    `/suppression-lists/${encodeURIComponent(listId)}/entries${qs({
      includeTotal: true,
      ...params,
    })}`,
  );

export const createSuppressionEntry = (
  listId: string,
  body: SuppressionEntryCreate,
): Promise<SuppressionEntryView> =>
  api.post<SuppressionEntryView>(
    `/suppression-lists/${encodeURIComponent(listId)}/entries`,
    body,
  );

export const deleteSuppressionEntry = (listId: string, entryId: string): Promise<void> =>
  api.delete(
    `/suppression-lists/${encodeURIComponent(listId)}/entries/${encodeURIComponent(entryId)}`,
  );

export const importSuppressionEntries = async (
  listId: string,
  csv: string,
): Promise<SuppressionImportResult> => {
  const res = await api.raw.post<SuppressionImportResult>(
    `/suppression-lists/${encodeURIComponent(listId)}/entries:import`,
    csv,
    { headers: { "Content-Type": "text/csv" } },
  );
  return res.data;
};

export const exportSuppressionEntries = async (listId: string): Promise<string> => {
  const res = await api.raw.get<string>(
    `/suppression-lists/${encodeURIComponent(listId)}/entries:export`,
    { responseType: "text" as "json" },
  );
  return typeof res.data === "string" ? res.data : String(res.data ?? "");
};
