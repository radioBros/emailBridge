import { beforeEach, describe, expect, it, vi } from "vitest";

const listAuditLog = vi.fn();

vi.mock("@/api/audit", () => ({
  listAuditLog: (...args: unknown[]) => listAuditLog(...args),
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k }),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

describe("useAuditLog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listAuditLog.mockResolvedValue({
      data: [
        {
          id: "aud_1",
          at: "2026-10-26T10:00:00Z",
          action: "member.invite",
          organizationId: "org_1",
          actor: { id: "usr_1", type: "user", ip: "203.0.113.1" },
          target: { id: "mem_1", type: "membership" },
          changes: { role: { before: null, after: "viewer" } },
        },
      ],
      nextCursor: null,
      total: 1,
    });
  });

  it("maps audit rows and actor/target labels", async () => {
    const { useAuditLog } = await import("@/features/organization/composables/useAuditLog");
    const a = useAuditLog();
    await a.load();
    expect(a.items.value[0].actor).toBe("user:usr_1");
    expect(a.items.value[0].target).toBe("membership:mem_1");
    expect(a.items.value[0].ip).toBe("203.0.113.1");
    expect(a.items.value[0].changes?.role?.after).toBe("viewer");
  });

  it("sends ISO from/to when filters use datetime-local", async () => {
    const { useAuditLog } = await import("@/features/organization/composables/useAuditLog");
    const a = useAuditLog();
    a.from.value = "2026-10-01T00:00";
    a.to.value = "2026-10-26T23:59";
    a.applyFilters();
    await vi.waitFor(() => expect(listAuditLog).toHaveBeenCalled());
    const arg = listAuditLog.mock.calls.at(-1)?.[0] as { from?: string; to?: string };
    expect(arg.from).toMatch(/Z$/);
    expect(arg.to).toMatch(/Z$/);
  });

  it("marks 403 as forbidden and filters locally", async () => {
    const { ApiProblemError } = await import("@/api/client");
    listAuditLog.mockRejectedValueOnce(
      new ApiProblemError({ status: 403, code: "forbidden", title: "no" }),
    );
    const { useAuditLog } = await import("@/features/organization/composables/useAuditLog");
    const a = useAuditLog();
    await a.load();
    expect(a.forbidden.value).toBe(true);

    listAuditLog.mockResolvedValueOnce({
      data: [
        {
          id: "aud_2",
          at: "2026-10-26T10:00:00Z",
          action: "api_key.create",
          organizationId: "org_1",
          actor: { id: "usr_2", type: "user" },
          target: { id: "key_1", type: "api_key" },
        },
      ],
      nextCursor: null,
      total: 1,
    });
    a.forbidden.value = false;
    a.search.value = "missing";
    await a.load();
    expect(a.items.value).toHaveLength(0);
  });
});

