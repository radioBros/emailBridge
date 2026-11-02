import { api } from "@/api/client";

/** CloudEvents envelope (OpenAPI Event — local until schema.d.ts regenerates). */
export type EbEvent = {
  specversion: string;
  id: string;
  type: string;
  source: string;
  subject: string;
  time: string;
  datacontenttype: string;
  dataschema: string;
  eborg: string;
  ebaccount?: string;
  ebschema: number;
  traceparent?: string;
  data: Record<string, unknown>;
};

export type EventList = {
  data: EbEvent[] | null;
  nextCursor?: string | null;
  total?: number;
  totalCapped?: boolean;
};

export interface EventListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  type?: string;
  accountId?: string;
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

export const listEvents = (query: EventListQuery = {}): Promise<EventList> =>
  api.get<EventList>(
    `/events${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort ?? "-time",
      type: query.type,
      accountId: query.accountId,
      from: query.from,
      to: query.to,
    })}`,
  );

export const getEvent = (id: string): Promise<EbEvent> =>
  api.get<EbEvent>(`/events/${encodeURIComponent(id)}`);

/** Pending OpenAPI until Claude regenerates (P3.3b point-in-time replay). */
export type EventsReplayBody = {
  endpointId: string;
  from: string;
  to?: string;
  types?: string[];
  ignoreEndpointFilter?: boolean;
};

export type JobAccepted = {
  job: { id: string; kind: string; state: string; createdAt?: string };
};

export const replayEventsToEndpoint = (body: EventsReplayBody): Promise<JobAccepted> =>
  api.post<JobAccepted>("/events/replay", body);

/** Build the SSE URL for live events (caller opens with fetch + org header). */
export const eventsStreamPath = (query: {
  type?: string;
  accountId?: string;
  lastEventId?: string;
}): string => {
  const params = new URLSearchParams();
  if (query.type) params.set("type", query.type);
  if (query.accountId) params.set("accountId", query.accountId);
  if (query.lastEventId) params.set("lastEventId", query.lastEventId);
  const s = params.toString();
  return `/v1/events/stream${s ? `?${s}` : ""}`;
};
