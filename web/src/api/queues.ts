import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type QueueName =
  | "EMAILBRIDGE_SUBMIT"
  | "EMAILBRIDGE_DELIVERY"
  | "EMAILBRIDGE_SYSMAIL"
  | "EMAILBRIDGE_EVENTS";

export type QueueKind = components["schemas"]["QueueView"]["kind"];
export type QueueScope = "organization" | "all";

export type QueueItemCounts = components["schemas"]["QueuesCounts"];
export type QueueSummary = components["schemas"]["QueueView"];
export type QueueItem = components["schemas"]["QueuesItem"];
export type QueueItemPage = components["schemas"]["QueuesPage"];
export type QueueCancelBody = components["schemas"]["CancelRequest"];
export type QueueSkipped = components["schemas"]["Skipped"];
export type QueueCancelResult = components["schemas"]["CancelResult"];

export interface QueueFilter {
  state?: string;
  accountId?: string;
  endpointId?: string;
  createdFrom?: string;
  createdTo?: string;
  q?: string;
}

export interface QueueItemsQuery extends QueueFilter {
  scope?: QueueScope;
  page?: number;
  limit?: number;
  sort?: string;
}

const qs = (params: Record<string, string | number | undefined>): string => {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== "") p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
};

export const listQueueSummaries = async (scope: QueueScope = "organization"): Promise<QueueSummary[]> => {
  const body = await api.get<{ data?: QueueSummary[] | null }>(
    `/queues${qs({ scope: scope === "all" ? "all" : undefined })}`,
  );
  return body.data ?? [];
};

export const listQueueItems = (queue: string, query: QueueItemsQuery): Promise<QueueItemPage> =>
  api.get<QueueItemPage>(
    `/queues/${encodeURIComponent(queue)}/items${qs({
      scope: query.scope === "all" ? "all" : undefined,
      page: query.page,
      limit: query.limit,
      sort: query.sort,
      state: query.state,
      accountId: query.accountId,
      endpointId: query.endpointId,
      createdFrom: query.createdFrom,
      createdTo: query.createdTo,
      q: query.q,
    })}`,
  );

export const cancelQueueItems = (queue: string, body: QueueCancelBody): Promise<QueueCancelResult> =>
  api.post<QueueCancelResult>(`/queues/${encodeURIComponent(queue)}/items/cancel`, body);
