import { api } from "@/api/client";
import type { components } from "@/api/schema";

export type Organization = components["schemas"]["Organization"] & {
  memberCount?: number;
  accountCount?: number;
};
export type OrganizationList = Omit<
  components["schemas"]["OrganizationList"],
  "data"
> & {
  data: Organization[] | null;
};
export type OrgCreateInBody = components["schemas"]["OrgCreateInBody"];
export type OrgPatchInBody = components["schemas"]["OrgPatchInBody"];
export type OrgDeleteOutBody = components["schemas"]["OrgDeleteOutBody"];
export type MemberCreateInBody = components["schemas"]["MemberCreateInBody"];
export type Membership = components["schemas"]["Membership"];
export type Job = components["schemas"]["Job"];

export type OrgQuotaKey =
  | "accounts"
  | "sendsPerDay"
  | "sendsPerAccountMinute"
  | "endpoints";

export interface OrgListQuery {
  page?: number;
  limit?: number;
  includeTotal?: boolean;
  sort?: string;
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

const etag = (version: number): string => `"v${version}"`;

export const listOrganizations = (query: OrgListQuery = {}): Promise<OrganizationList> =>
  api.get<OrganizationList>(
    `/organizations${qs({
      page: query.page ?? 1,
      limit: query.limit ?? 25,
      includeTotal: query.includeTotal ?? true,
      sort: query.sort,
      q: query.q,
    })}`,
  );

export const createOrganization = (body: OrgCreateInBody): Promise<Organization> =>
  api.post<Organization>("/organizations", body);

export const getOrganization = (organizationId: string): Promise<Organization> =>
  api.get<Organization>(`/organizations/${encodeURIComponent(organizationId)}`);

export const patchOrganization = (
  organizationId: string,
  body: OrgPatchInBody,
  version: number,
): Promise<Organization> =>
  api.raw
    .patch<Organization>(`/organizations/${encodeURIComponent(organizationId)}`, body, {
      headers: { "If-Match": etag(version) },
    })
    .then((r) => r.data);

export const deleteOrganization = (
  organizationId: string,
  version: number,
): Promise<OrgDeleteOutBody> =>
  api.raw
    .delete<OrgDeleteOutBody>(`/organizations/${encodeURIComponent(organizationId)}`, {
      headers: { "If-Match": etag(version) },
    })
    .then((r) => r.data);

export const inviteMember = (
  organizationId: string,
  body: MemberCreateInBody,
): Promise<Membership> =>
  api.raw
    .post<Membership>("/members", body, {
      headers: { "EB-Organization": organizationId },
    })
    .then((r) => r.data);

export const getJob = (jobId: string): Promise<Job> =>
  api.get<Job>(`/jobs/${encodeURIComponent(jobId)}`);
