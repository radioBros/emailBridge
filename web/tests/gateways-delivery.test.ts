import { beforeEach, describe, expect, it, vi } from "vitest";
import { useDeliveryTest } from "@/features/gateways/composables/useDeliveryTest";

vi.mock("@/api/accounts", () => ({
  listAccounts: vi.fn(),
}));

vi.mock("@/api/deliveryTests", () => ({
  createDeliveryTest: vi.fn(),
  getDeliveryTest: vi.fn(),
  isTerminalDeliveryTest: (s: string) => ["succeeded", "failed", "timedOut"].includes(s),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (e: unknown) => (e instanceof Error ? e.message : "err"),
    fieldErrors: () => ({}),
  }),
}));

import { listAccounts } from "@/api/accounts";
import { createDeliveryTest, getDeliveryTest } from "@/api/deliveryTests";

describe("useDeliveryTest", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("requires account and to", async () => {
    const d = useDeliveryTest();
    const res = await d.start();
    expect(res).toBeNull();
    expect(d.error.value).toBe("required");
  });

  it("loads accounts and polls to terminal", async () => {
    vi.mocked(listAccounts).mockResolvedValue({
      data: [
        {
          id: "acc_1",
          name: "Inbox",
          email: "a@test",
          organizationId: "org_1",
          state: "connected",
          createdAt: "2026-10-01T00:00:00Z",
          updatedAt: "2026-10-01T00:00:00Z",
          version: 1,
        } as never,
      ],
      total: 1,
    });
    vi.mocked(createDeliveryTest).mockResolvedValue({
      id: "dtt_1",
      jobId: "job_1",
      state: "queued",
    });
    vi.mocked(getDeliveryTest)
      .mockResolvedValueOnce({
        id: "dtt_1",
        organizationId: "org_1",
        accountId: "acc_1",
        to: "seed@test",
        state: "running",
        createdAt: "2026-10-26T00:00:00Z",
      })
      .mockResolvedValueOnce({
        id: "dtt_1",
        organizationId: "org_1",
        accountId: "acc_1",
        to: "seed@test",
        state: "succeeded",
        folder: "inbox",
        latencyMs: 900,
        createdAt: "2026-10-26T00:00:00Z",
      });

    const d = useDeliveryTest();
    await d.loadAccounts();
    expect(d.accountLabel("acc_1")).toBe("Inbox");
    d.accountId.value = "acc_1";
    d.to.value = "seed@test";
    const view = await d.start();
    expect(view?.state).toBe("succeeded");
    expect(d.result.value?.folder).toBe("inbox");
  });
});
