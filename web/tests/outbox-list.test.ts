import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

const routeState = vi.hoisted(() => ({
  query: {} as Record<string, string>,
  replace: (() => undefined) as (to: unknown) => void,
}));

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: routeState.query }),
  useRouter: () => ({ replace: (to: unknown) => routeState.replace(to) }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => String(err),
    fieldErrors: () => ({}),
  }),
}));

vi.mock("@/api/outbox", () => ({
  listOutboundMessages: vi.fn(async () => ({
    data: [
      {
        id: "out_1",
        organizationId: "org_1",
        accountId: "acc_1",
        state: "queued",
        policy: "standard",
        messageId: "<m@test>",
        messageIdHeader: "<m@test>",
        envelope: { from: "a@test", to: ["b@test"] },
        copyToSent: { enabled: true, state: "pending" },
        createdAt: "2026-10-26T10:00:00Z",
        size: 100,
        maxAttempts: 5,
        version: 1,
        subject: "Hello",
        attempts: [],
      },
    ],
    nextCursor: null,
    total: 1,
  })),
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
  getOutboundMessage: vi.fn(),
  cancelOutboundMessage: vi.fn(),
}));

import { listOutboundMessages } from "@/api/outbox";
import { useOutboxList } from "@/features/outbox/composables/useOutboxList";
import { canCancelOutbound } from "@/features/outbox/helpers/outboundState";

describe("useOutboxList", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    routeState.query = {};
    routeState.replace = () => undefined;
  });

  it("sends the date range as createdFrom / createdTo, never as the from / to address filters", async () => {
    routeState.query = {
      state: "sent",
      createdFrom: "2026-10-26T10:00:00Z",
      createdTo: "2026-10-27T10:00:00Z",
    };
    const replaced: unknown[] = [];
    routeState.replace = (to) => replaced.push(to);
    const { load, filterFrom, filterTo, activeFilterCount } = useOutboxList();
    expect(filterFrom.value).toBe("2026-10-26T10:00:00Z");
    expect(filterTo.value).toBe("2026-10-27T10:00:00Z");
    expect(activeFilterCount.value).toBe(2);
    await load();
    const query = vi.mocked(listOutboundMessages).mock.calls[0]![0]!;
    expect(query.createdFrom).toBe("2026-10-26T10:00:00.000Z");
    expect(query.createdTo).toBe("2026-10-27T10:00:00.000Z");
    expect(query.from).toBeUndefined();
    expect(query.to).toBeUndefined();
    expect(query.state).toBe("sent");
    // The URL keeps the same names, so a dashboard link round-trips.
    expect(replaced.at(-1)).toEqual({
      query: { state: "sent", createdFrom: "2026-10-26T10:00:00Z", createdTo: "2026-10-27T10:00:00Z" },
    });
  });

  it("loads outbound rows", async () => {
    const { items, total, load, isFirstLoad, empty, noResults } = useOutboxList();
    expect(isFirstLoad.value).toBe(false);
    const pending = load();
    expect(isFirstLoad.value).toBe(true);
    await pending;
    expect(listOutboundMessages).toHaveBeenCalled();
    expect(items.value).toHaveLength(1);
    expect(total.value).toBe(1);
    expect(empty.value).toBe(false);
    expect(noResults.value).toBe(false);
    expect(canCancelOutbound(items.value[0]!.state)).toBe(true);
  });
});
