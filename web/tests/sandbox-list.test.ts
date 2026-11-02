import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => String(err),
    fieldErrors: () => ({}),
  }),
}));

vi.mock("@/api/outbox", () => ({
  searchAccounts: vi.fn(async () => [
    {
      id: "acc_1",
      name: "Support",
      email: "a@test",
      organizationId: "org_1",
      state: "connected",
      createdAt: "2026-01-01T00:00:00Z",
      updatedAt: "2026-01-01T00:00:00Z",
      version: 1,
      imap: {},
      smtp: {},
      sync: {},
    },
  ]),
}));

vi.mock("@/api/sandbox", () => ({
  listSandboxMessages: vi.fn(async () => ({
    data: [
      {
        id: "cap_1",
        organizationId: "org_1",
        accountId: "acc_1",
        outboundId: "out_1",
        envelope: { from: "a@test", to: ["b@test"] },
        subject: "Captured",
        capturedAt: "2026-10-26T11:00:00Z",
      },
    ],
    nextCursor: null,
    total: 1,
  })),
  searchAccounts: vi.fn(async () => []),
  getSandboxMessage: vi.fn(),
  getSandboxMessageRaw: vi.fn(),
  deleteSandboxMessage: vi.fn(),
  downloadSandboxRaw: vi.fn(),
}));

import { listSandboxMessages } from "@/api/sandbox";
import { useSandboxList } from "@/features/sandbox/composables/useSandboxList";

describe("useSandboxList", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("loads sandbox rows and can remove locally", async () => {
    const { items, total, load, removeLocal, empty, noResults } = useSandboxList();
    await load();
    expect(listSandboxMessages).toHaveBeenCalled();
    expect(items.value).toHaveLength(1);
    expect(total.value).toBe(1);
    expect(empty.value).toBe(false);
    expect(noResults.value).toBe(false);
    removeLocal("cap_1");
    expect(items.value).toHaveLength(0);
    expect(total.value).toBe(0);
    expect(empty.value).toBe(true);
  });
});
