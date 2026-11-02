import { beforeEach, describe, expect, it, vi } from "vitest";
import { isValidOrgSlug, slugifyOrgName } from "@/features/organizations/helpers/slug";
import { orgStatusColor } from "@/features/organizations/helpers/status";

const listOrganizations = vi.fn();
const createOrganization = vi.fn();
const inviteMember = vi.fn();
const patchOrganization = vi.fn();
const deleteOrganization = vi.fn();
const getJob = vi.fn();

vi.mock("@/api/organizations", () => ({
  listOrganizations: (...args: unknown[]) => listOrganizations(...args),
  createOrganization: (...args: unknown[]) => createOrganization(...args),
  inviteMember: (...args: unknown[]) => inviteMember(...args),
  patchOrganization: (...args: unknown[]) => patchOrganization(...args),
  deleteOrganization: (...args: unknown[]) => deleteOrganization(...args),
  getJob: (...args: unknown[]) => getJob(...args),
}));

vi.mock("@/features/deliveries/helpers/jobPoll", () => ({
  pollJob: vi.fn(async (id: string, onUpdate: (j: { id: string; state: string }) => void) => {
    const job = { id, state: "succeeded" as const };
    onUpdate(job);
    return job;
  }),
  jobProgressText: (j: { state: string }) => j.state,
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k }),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

const profile = { instanceRole: "owner" as string };
vi.mock("@/stores/session", () => ({
  useSessionStore: () => ({
    get profile() {
      return profile;
    },
    organizationId: "org_current",
  }),
}));

const notifyPush = vi.fn();
vi.mock("@/stores/notify", () => ({
  useNotifyStore: () => ({ push: notifyPush }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) =>
      err && typeof err === "object" && "problem" in err
        ? ((err as { problem: { detail?: string } }).problem.detail ?? "err")
        : "err",
  }),
}));

describe("organizations helpers", () => {
  it("slugifies names and validates", () => {
    expect(slugifyOrgName("Acme Legal!")).toBe("acme-legal");
    expect(slugifyOrgName("A")).toBe("a0");
    expect(isValidOrgSlug("acme-legal")).toBe(true);
    expect(isValidOrgSlug("A")).toBe(false);
  });

  it("maps status colours", () => {
    expect(orgStatusColor("active")).toBe("success");
    expect(orgStatusColor("suspended")).toBe("warning");
    expect(orgStatusColor("deleting")).toBe("error");
  });
});

describe("useOrganizations", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    profile.instanceRole = "owner";
    listOrganizations.mockResolvedValue({
      data: [
        {
          id: "org_1",
          name: "Acme",
          slug: "acme",
          status: "active",
          memberCount: 2,
          accountCount: 5,
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 1,
          quotas: {},
        },
      ],
      nextCursor: null,
      total: 1,
    });
  });

  it("loads list rows for an instance owner", async () => {
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    await o.load();
    expect(o.items.value).toHaveLength(1);
    expect(o.items.value[0].memberCount).toBe(2);
    expect(o.forbidden.value).toBe(false);
  });

  it("gates non-owners", async () => {
    profile.instanceRole = "none";
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    await o.load();
    expect(o.forbidden.value).toBe(true);
    expect(listOrganizations).not.toHaveBeenCalled();
  });

  it("creates and invites the first owner", async () => {
    createOrganization.mockResolvedValueOnce({
      id: "org_new",
      name: "Beta",
      slug: "beta",
      status: "active",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
      version: 1,
    });
    inviteMember.mockResolvedValueOnce({});
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    const ok = await o.create({ name: "Beta", slug: "beta" }, "owner@example.test");
    expect(ok).toBe(true);
    expect(createOrganization).toHaveBeenCalledWith({ name: "Beta", slug: "beta" });
    expect(inviteMember).toHaveBeenCalledWith("org_new", {
      email: "owner@example.test",
      role: "owner",
    });
  });

  it("surfaces slug conflict as field error", async () => {
    const { ApiProblemError } = await import("@/api/client");
    createOrganization.mockRejectedValueOnce(
      new ApiProblemError({ status: 409, code: "conflict", title: "taken" }),
    );
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    expect(await o.create({ name: "X", slug: "x" })).toBe(false);
    expect(o.createSlugError.value).toBe("organizations.slug_taken");
  });

  it("blocks delete of the active organisation", async () => {
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    await o.load();
    const row = { ...o.items.value[0], id: "org_current" };
    expect(await o.remove(row)).toBe(false);
    expect(deleteOrganization).not.toHaveBeenCalled();
  });

  it("deletes with typed confirm path polling the job", async () => {
    deleteOrganization.mockResolvedValueOnce({
      job: { id: "job_1", kind: "organization.erasure", state: "queued", createdAt: "2026-01-01T00:00:00Z" },
    });
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    await o.load();
    expect(await o.remove(o.items.value[0])).toBe(true);
    expect(deleteOrganization).toHaveBeenCalledWith("org_1", 1);
  });

  it("saves quotas payload", async () => {
    patchOrganization.mockResolvedValueOnce({});
    const { useOrganizations } = await import(
      "@/features/organizations/composables/useOrganizations"
    );
    const o = useOrganizations();
    await o.load();
    expect(
      await o.saveQuotas(o.items.value[0], { accounts: 10, sendsPerDay: null }),
    ).toBe(true);
    expect(patchOrganization).toHaveBeenCalledWith(
      "org_1",
      { quotas: { accounts: 10, sendsPerDay: null } },
      1,
    );
  });
});
