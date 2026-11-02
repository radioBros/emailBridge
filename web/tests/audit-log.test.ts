import { beforeEach, describe, expect, it, vi } from "vitest";

const listAuditLog = vi.fn();

vi.mock("@/api/audit", () => ({
  listAuditLog: (...args: unknown[]) => listAuditLog(...args),
  auditCsvExportAvailable: false,
}));

vi.mock("vue-i18n", () => ({
  useI18n: () => ({ t: (k: string) => k }),
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

describe("useAuditLog (P3.4a)", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    listAuditLog.mockResolvedValue({
      data: [
        {
          id: "aud_1",
          at: "2026-10-26T10:00:00Z",
          action: "apikey.update",
          organizationId: "org_1",
          actor: { id: "usr_1", type: "user", ip: "203.0.113.1" },
          target: { id: "key_1", type: "apiKey" },
          changes: { scopes: { before: ["admin"], after: ["accounts:read"] } },
        },
      ],
      nextCursor: null,
      total: 1,
    });
  });

  it("sends targetId resource filter", async () => {
    const { useAuditLog } = await import("@/features/organization/composables/useAuditLog");
    const a = useAuditLog();
    a.targetId.value = "key_1";
    a.applyFilters();
    await vi.waitFor(() => expect(listAuditLog).toHaveBeenCalled());
    const arg = listAuditLog.mock.calls.at(-1)?.[0] as { targetId?: string };
    expect(arg.targetId).toBe("key_1");
  });

  it("maps change diffs for the detail drawer", async () => {
    const { useAuditLog } = await import("@/features/organization/composables/useAuditLog");
    const a = useAuditLog();
    await a.load();
    expect(a.items.value[0].changes?.scopes?.after).toEqual(["accounts:read"]);
  });
});
