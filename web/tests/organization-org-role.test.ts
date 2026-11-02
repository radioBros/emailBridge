import { describe, expect, it, vi } from "vitest";

vi.mock("@/stores/session", () => ({
  useSessionStore: () => ({
    me: { role: "admin" },
  }),
}));

describe("useOrgRole", () => {
  it("exposes capabilities for the session role", async () => {
    const { useOrgRole } = await import("@/features/organization/composables/useOrgRole");
    const org = useOrgRole();
    expect(org.role.value).toBe("admin");
    expect(org.canWriteMembers.value).toBe(true);
    expect(org.canWriteApiKeys.value).toBe(true);
    expect(org.canReadAudit.value).toBe(true);
    expect(org.assignableRoles.value).not.toContain("owner");
    expect(org.canEditMember("owner")).toBe(false);
    expect(org.canEditMember("viewer")).toBe(true);
  });
});
