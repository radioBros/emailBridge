import { beforeEach, describe, expect, it, vi } from "vitest";

const listMembers = vi.fn();
const inviteMember = vi.fn();
const patchMember = vi.fn();
const removeMember = vi.fn();

vi.mock("@/api/organization", () => ({
  listMembers: (...args: unknown[]) => listMembers(...args),
  inviteMember: (...args: unknown[]) => inviteMember(...args),
  patchMember: (...args: unknown[]) => patchMember(...args),
  removeMember: (...args: unknown[]) => removeMember(...args),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k, locale: { value: "en" } }),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

vi.mock("@/stores/notify", () => ({
  useNotifyStore: () => ({ push: vi.fn() }),
}));

describe("useMembers", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listMembers.mockResolvedValue({
      data: [
        {
          id: "mem_1",
          organizationId: "org_1",
          email: "ada@example.test",
          role: "admin",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 1,
          acceptedAt: "2026-01-02T00:00:00Z",
        },
      ],
      nextCursor: null,
      total: 1,
    });
  });

  it("loads and maps member rows", async () => {
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    await m.load();
    expect(m.items.value).toHaveLength(1);
    expect(m.items.value[0].email).toBe("ada@example.test");
    expect(m.items.value[0].name).toBe("ada@example.test");
    expect(m.total.value).toBe(1);
    expect(m.forbidden.value).toBe(false);
  });

  it("marks 403 as forbidden", async () => {
    const { ApiProblemError } = await import("@/api/client");
    listMembers.mockRejectedValueOnce(
      new ApiProblemError({ status: 403, code: "forbidden", title: "no" }),
    );
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    await m.load();
    expect(m.forbidden.value).toBe(true);
    expect(m.items.value).toHaveLength(0);
  });

  it("invites a member then reloads", async () => {
    inviteMember.mockResolvedValueOnce({});
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    const ok = await m.invite("new@example.test", "viewer");
    expect(ok).toBe(true);
    expect(inviteMember).toHaveBeenCalledWith({ email: "new@example.test", role: "viewer" });
    expect(listMembers).toHaveBeenCalled();
  });

  it("changes role and removes members", async () => {
    patchMember.mockResolvedValueOnce({});
    removeMember.mockResolvedValueOnce(undefined);
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    await m.load();
    const row = m.items.value[0];
    expect(await m.changeRole(row, "operator")).toBe(true);
    expect(patchMember).toHaveBeenCalledWith(row.id, { role: "operator" }, row.version);
    expect(await m.remove(row)).toBe(true);
    expect(removeMember).toHaveBeenCalledWith(row.id, row.version);
  });

  it("returns false when invite fails", async () => {
    inviteMember.mockRejectedValueOnce(new Error("nope"));
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    expect(await m.invite("x@y.z", "viewer")).toBe(false);
  });

  it("sends q to the API on search", async () => {
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    m.onSearch("ada");
    await vi.waitFor(() => expect(listMembers).toHaveBeenCalled());
    const arg = listMembers.mock.calls.at(-1)?.[0] as { q?: string; page?: number };
    expect(arg.q).toBe("ada");
    expect(arg.page).toBe(1);
  });

  it("maps live name and lastLoginAt", async () => {
    listMembers.mockResolvedValueOnce({
      data: [
        {
          id: "mem_1",
          organizationId: "org_1",
          email: "ada@example.test",
          name: "Ada Lovelace",
          lastLoginAt: "2026-10-25T18:00:00Z",
          role: "owner",
          acceptedAt: "2026-01-01T00:00:00Z",
          createdAt: "2026-01-01T00:00:00Z",
          updatedAt: "2026-01-01T00:00:00Z",
          version: 1,
        },
      ],
      nextCursor: null,
      total: 1,
    });
    const { useMembers } = await import("@/features/organization/composables/useMembers");
    const m = useMembers();
    await m.load();
    expect(m.items.value[0].name).toBe("Ada Lovelace");
    expect(m.items.value[0].lastLoginAt).toBe("2026-10-25T18:00:00Z");
  });
});


