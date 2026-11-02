import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type Route = components["schemas"]["Route"];
export type RouteCreate = components["schemas"]["RouteCreate"];
export type RouteUpdate = components["schemas"]["RouteUpdate"];
export type RulesListResult = components["schemas"]["RulesListResult"];
export type RouteTestRequest = components["schemas"]["RouteTestRequest"];
export type RouteTestResult = components["schemas"]["RouteTestResult"];
export type Job = components["schemas"]["Job"];
export type JobAccepted = components["schemas"]["JobAccepted"];

/** Pending OpenAPI field (brief 19b / follow-up common §C). */
export type RouteLastError = {
  code: string;
  message: string;
  at: string;
};

export type RouteRow = Route & {
  lastError?: RouteLastError | null;
};

export type RulesListResultRow = Omit<RulesListResult, "data"> & {
  data: RouteRow[] | null;
};

export interface RouteListParams {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
  cursor?: string;
  q?: string;
}

/** local until schema regen — P3.1 builder / versions / dry-run */
export type RuleBuilderMatch = "all" | "any";
export type RuleBuilderOp =
  | "eq"
  | "neq"
  | "in"
  | "contains"
  | "startsWith"
  | "endsWith"
  | "matches"
  | "exists"
  | "notExists";

export type RuleBuilderCondition = {
  field: string;
  name?: string;
  op: RuleBuilderOp;
  value?: unknown;
};

export type RuleBuilder = {
  match: RuleBuilderMatch;
  conditions: RuleBuilderCondition[];
};

export type BuilderCompileResult = {
  cel: string;
  ok: boolean;
  error?: string;
};

export type BuilderExpressibleResult = {
  expressible: boolean;
  builder?: RuleBuilder;
};

export type RouteVersion = {
  id: string;
  organizationId: string;
  routeId: string;
  version: number;
  filter: string;
  map?: string;
  builder?: Record<string, unknown>;
  createdBy: string;
  createdAt: string;
};

export type RouteVersionList = {
  data: RouteVersion[];
  total?: number;
  totalCapped?: boolean;
};

export type RouteDryRunRequest = {
  windowHours?: number;
  limit?: number;
  filter?: string;
  map?: string;
  builder?: RuleBuilder;
};

export type RouteDryRunResult = {
  matched: number;
  scanned: number;
  samples: Array<{ eventId: string; mapped?: unknown }>;
  errorsByPhase: Record<string, number>;
  cost: { total: number; max: number; avg: number };
  windowHours: number;
  limit: number;
};

const qs = (params: RouteListParams): string => {
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

export const listRoutes = (params: RouteListParams = {}): Promise<RulesListResultRow> =>
  api.get<RulesListResultRow>(`/routes${qs({ includeTotal: true, ...params })}`);

export const getRoute = (id: string): Promise<Route> =>
  api.get<Route>(`/routes/${encodeURIComponent(id)}`);

export const createRoute = (body: RouteCreate): Promise<Route> =>
  api.post<Route>("/routes", body);

export const patchRoute = (id: string, body: RouteUpdate): Promise<Route> =>
  api.patch<Route>(`/routes/${encodeURIComponent(id)}`, body);

export const deleteRoute = (id: string): Promise<void> =>
  api.delete(`/routes/${encodeURIComponent(id)}`);

export const testRoute = (id: string, body: RouteTestRequest): Promise<RouteTestResult> =>
  api.post<RouteTestResult>(`/routes/${encodeURIComponent(id)}/test`, body);

export const compileBuilder = (builder: RuleBuilder): Promise<BuilderCompileResult> =>
  api.post<BuilderCompileResult>("/routes/builder/compile", { builder });

export const expressibleBuilder = (cel: string): Promise<BuilderExpressibleResult> =>
  api.post<BuilderExpressibleResult>("/routes/builder/expressible", { cel });

export const listRouteVersions = (
  routeId: string,
  params: { page?: number; limit?: number; includeTotal?: boolean } = {},
): Promise<RouteVersionList> => {
  const sp = new URLSearchParams();
  if (params.page != null) sp.set("page", String(params.page));
  if (params.limit != null) sp.set("limit", String(params.limit));
  if (params.includeTotal != null) sp.set("includeTotal", String(params.includeTotal));
  const q = sp.toString();
  return api.get<RouteVersionList>(
    `/routes/${encodeURIComponent(routeId)}/versions${q ? `?${q}` : ""}`,
  );
};

export const restoreRouteVersion = (routeId: string, version: number): Promise<Route> =>
  api.post<Route>(
    `/routes/${encodeURIComponent(routeId)}/versions/${version}/restore`,
    {},
  );

export const dryRunRoute = (
  routeId: string,
  body: RouteDryRunRequest = {},
): Promise<JobAccepted> =>
  api.post<JobAccepted>(`/routes/${encodeURIComponent(routeId)}/dry-run`, body);

export const getJob = (jobId: string): Promise<Job> =>
  api.get<Job>(`/jobs/${encodeURIComponent(jobId)}`);
