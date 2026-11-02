import { api } from "@/api/client";
import { patchSettings } from "@/api/settings";

// local until schema regen — W73 endpoint rules, stats, decisions, test events, named lists (W73b drafts).

/** Builder group: all (&&), any (||) or not (!(…)). */
export type RuleMatch = "all" | "any" | "not";

export interface RuleLeaf {
  field: string;
  /** Header name, list name, or the IANA zone of weekday / hour. */
  name?: string;
  op: string;
  value?: unknown;
}

export interface RuleGroup {
  match: RuleMatch;
  conditions: RuleNode[];
}

export type RuleNode = RuleLeaf | RuleGroup;

export interface EndpointRule {
  filter?: string;
  map?: string;
  builder?: RuleGroup | Record<string, unknown>;
  currentVersion?: number;
}

export interface RuleTestRequest {
  event?: Record<string, unknown>;
  eventId?: string;
  sample?: string;
  /** W73b draft: evaluated instead of the stored rule. */
  filter?: string;
  map?: string;
  builder?: RuleGroup;
}

export interface RuleTraceTerm {
  expr: string;
  result: boolean;
}

export interface RuleErrorAt {
  path: "filter" | "map";
  line: number;
  column: number;
}

export interface RuleTestResult {
  matched: boolean;
  mapped?: unknown;
  cost: number;
  costUsed?: number;
  durationMs: number;
  error?: string;
  errorAt?: RuleErrorAt;
  trace?: RuleTraceTerm[] | null;
}

export interface RuleStats {
  evaluated: number;
  matched: number;
  skipped: number;
  errors: number;
  lastMatchedAt?: string;
  lastErrorAt?: string;
  lastError?: string;
}

export interface RuleDecision {
  eventId: string;
  matched: boolean;
  reason?: string;
  error?: string;
  at: string;
}

export interface RuleTestEvent {
  id: string;
  type: string;
  time: string;
  accountId?: string;
  subject?: string;
}

export interface RuleVersion {
  id: string;
  version: number;
  filter: string;
  map?: string;
  builder?: Record<string, unknown>;
  createdBy?: string;
  createdAt: string;
}

export interface RuleVersionList {
  data: RuleVersion[];
  total?: number;
}

export type RuleListKind = "addresses" | "domains" | "keywords";

export interface RuleList {
  id: string;
  organizationId: string;
  name: string;
  kind: RuleListKind;
  entries?: string[];
  entryCount: number;
  createdAt: string;
  updatedAt: string;
  version: number;
}

export interface RuleListPage {
  data: RuleList[];
  total?: number;
}

export interface RuleListQuery {
  page?: number;
  limit?: number;
  q?: string;
  kind?: RuleListKind;
  sort?: string;
  includeTotal?: boolean;
}

export interface BuilderCompileResult {
  cel: string;
  ok: boolean;
  error?: string;
}

/** Which kind of rule the editor works on. */
export type RuleTargetKind = "endpoint" | "route";

const qs = (params: Record<string, string | number | boolean | undefined>): string => {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === "") continue;
    sp.set(k, String(v));
  }
  const s = sp.toString();
  return s ? `?${s}` : "";
};

const enc = encodeURIComponent;

/** Base path of the rule resource: an endpoint's `/rule` or the route itself. */
export const ruleBase = (kind: RuleTargetKind, id: string): string =>
  kind === "endpoint" ? `/webhook-endpoints/${enc(id)}/rule` : `/routes/${enc(id)}`;

export const testRule = (
  kind: RuleTargetKind,
  id: string,
  body: RuleTestRequest,
): Promise<RuleTestResult> => api.post<RuleTestResult>(`${ruleBase(kind, id)}/test`, body);

export const getRuleStats = (kind: RuleTargetKind, id: string): Promise<RuleStats> =>
  api.get<RuleStats>(`${ruleBase(kind, id)}/stats`);

export const listRuleDecisions = (
  kind: RuleTargetKind,
  id: string,
  limit = 50,
): Promise<{ data: RuleDecision[] }> =>
  api.get<{ data: RuleDecision[] }>(`${ruleBase(kind, id)}/decisions${qs({ limit })}`);

export const listRuleVersions = (
  kind: RuleTargetKind,
  id: string,
  params: { page?: number; limit?: number } = {},
): Promise<RuleVersionList> =>
  api.get<RuleVersionList>(
    `${ruleBase(kind, id)}/versions${qs({ includeTotal: true, ...params })}`,
  );

/** Restores a version as the new current one (If-Match = the resource version). */
export const restoreRuleVersion = async <T>(
  kind: RuleTargetKind,
  id: string,
  version: number,
  etag?: number,
): Promise<T> => {
  const res = await api.raw.post<T>(
    `${ruleBase(kind, id)}/versions/${version}/restore`,
    {},
    { headers: etag ? { "If-Match": `"v${etag}"` } : undefined },
  );
  return res.data;
};

export const listTestEvents = (
  params: { type?: string; accountId?: string; limit?: number } = {},
): Promise<{ data: RuleTestEvent[] }> =>
  api.get<{ data: RuleTestEvent[] }>(`/routes/test-events${qs({ limit: 30, ...params })}`);

export const getStoredEvent = (id: string): Promise<Record<string, unknown>> =>
  api.get<Record<string, unknown>>(`/events/${enc(id)}`);

export const compileRuleBuilder = (builder: RuleGroup): Promise<BuilderCompileResult> =>
  api.post<BuilderCompileResult>("/routes/builder/compile", { builder });

/** PATCH the endpoint's rule (and, when given, its event subscription); `rule: {}` removes the rule. */
export const saveEndpointRule = async <T>(
  id: string,
  rule: EndpointRule,
  etag?: number,
  events?: string[] | null,
): Promise<T> => {
  const res = await api.raw.patch<T>(
    `/webhook-endpoints/${enc(id)}`,
    events ? { rule, events } : { rule },
    { headers: etag ? { "If-Match": `"v${etag}"` } : undefined },
  );
  return res.data;
};

export const listRuleLists = (query: RuleListQuery = {}): Promise<RuleListPage> =>
  api.get<RuleListPage>(`/rule-lists${qs({ includeTotal: true, ...query })}`);

export const getRuleList = (id: string): Promise<RuleList> =>
  api.get<RuleList>(`/rule-lists/${enc(id)}`);

export const createRuleList = (body: {
  name: string;
  kind: RuleListKind;
  entries?: string[];
}): Promise<RuleList> => api.post<RuleList>("/rule-lists", body);

export const updateRuleList = async (
  id: string,
  body: { name?: string; entries?: string[] },
  version?: number,
): Promise<RuleList> => {
  const res = await api.raw.patch<RuleList>(`/rule-lists/${enc(id)}`, body, {
    headers: version ? { "If-Match": `"v${version}"` } : undefined,
  });
  return res.data;
};

export const deleteRuleList = async (id: string, version?: number): Promise<void> => {
  await api.raw.delete(`/rule-lists/${enc(id)}`, {
    headers: version ? { "If-Match": `"v${version}"` } : undefined,
  });
};

/** Stores a secret `settings.env` value (encrypted, masked everywhere); write-only. */
export const storeEnvSecret = (name: string, value: string): Promise<unknown> =>
  patchSettings("organization", { env: { [name]: { value, secret: true } } });
