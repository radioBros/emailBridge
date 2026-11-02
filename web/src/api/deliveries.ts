import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type Delivery = components["schemas"]["Delivery"] & {
  /** Pending OpenAPI fields until Claude regenerates schema.d.ts. */
  lastAttemptAt?: string;
  replayedAt?: string;
};
export type DeliveryAttempt = components["schemas"]["DeliveryAttempt"];
export type DeliveryList = Omit<components["schemas"]["DeliveryList"], "data"> & {
  data: Delivery[] | null;
};
export type Job = components["schemas"]["Job"];

/** Pending OpenAPI until Claude regenerates (P3 bulk replay). */
export type BulkReplayBody = {
  endpointId?: string;
  routeId?: string;
  eventType?: string;
  accountId?: string;
  state?: string;
  from?: string;
  to?: string;
  ids?: string[];
  includeReplayed?: boolean;
};

export type JobAccepted = {
  job: Job;
};

export interface DeliveryListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  q?: string;
  endpointId?: string;
  routeId?: string;
  eventType?: string;
  accountId?: string;
  state?: string;
  from?: string;
  to?: string;
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

export const listDeliveries = (query: DeliveryListQuery = {}): Promise<DeliveryList> =>
  api.get<DeliveryList>(
    `/deliveries${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "-createdAt",
      q: query.q,
      endpointId: query.endpointId,
      routeId: query.routeId,
      eventType: query.eventType,
      accountId: query.accountId,
      state: query.state,
      from: query.from,
      to: query.to,
    })}`,
  );

export const getDelivery = (id: string): Promise<Delivery> =>
  api.get<Delivery>(`/deliveries/${encodeURIComponent(id)}`);

export const replayDelivery = (id: string): Promise<Delivery> =>
  api.post<Delivery>(`/deliveries/${encodeURIComponent(id)}/replay`, {});

export const bulkReplayDeliveries = (body: BulkReplayBody): Promise<JobAccepted> =>
  api.post<JobAccepted>("/deliveries/replay", body);

export const getJob = (jobId: string): Promise<Job> =>
  api.get<Job>(`/jobs/${encodeURIComponent(jobId)}`);
