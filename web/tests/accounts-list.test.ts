import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {}, params: {} }),
  useRouter: () => ({ replace: vi.fn(), push: vi.fn() }),
}));

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({
      t: (k: string) => k,
      te: () => true,
      locale: { value: "en" },
    }),
  };
});

const realtimeHandlers: Array<(msg: unknown) => void> = [];
vi.mock("@/composables/useRealtime", () => ({
  useRealtime: (opts?: { onMessage?: (msg: unknown) => void }) => {
    if (opts?.onMessage) realtimeHandlers.push(opts.onMessage);
    return { status: { value: "idle" }, lastMessage: { value: null } };
  },
}));

vi.mock("@/api/accounts", () => ({
  listAccounts: vi.fn(async () => ({
    data: [
      {
        id: "acc_1",
        name: "A",
        email: "a@b.co",
        state: "connected",
        version: 1,
      },
    ],
    nextCursor: null,
    total: 1,
  })),
  syncAccount: vi.fn(async () => ({ job: { id: "j1" } })),
  reconnectAccount: vi.fn(async () => ({ job: { id: "j1" } })),
  resumeAccount: vi.fn(async () => ({ id: "acc_1", state: "connected" })),
  deleteAccount: vi.fn(async () => ({ job: { id: "j1" } })),
}));

import {
  deleteAccount,
  listAccounts,
  reconnectAccount,
  resumeAccount,
  syncAccount,
} from "@/api/accounts";
import { useAccountsList } from "@/features/accounts/composables/useAccountsList";

describe("useAccountsList", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    realtimeHandlers.length = 0;
  });

  it("loads accounts into items", async () => {
    const { items, total, load, isFirstLoad } = useAccountsList();
    const pending = load();
    expect(isFirstLoad.value).toBe(true);
    await pending;
    expect(listAccounts).toHaveBeenCalled();
    expect(items.value).toHaveLength(1);
    expect(total.value).toBe(1);
    expect(isFirstLoad.value).toBe(false);
  });

  it("runs sync reconnect resume delete", async () => {
    const { load, doSync, doReconnect, doResume, doDelete, items } = useAccountsList();
    await load();
    await doSync("acc_1", "fast");
    expect(syncAccount).toHaveBeenCalledWith("acc_1", { mode: "fast" });
    await doReconnect("acc_1");
    expect(reconnectAccount).toHaveBeenCalledWith("acc_1");
    await doResume("acc_1");
    expect(resumeAccount).toHaveBeenCalledWith("acc_1");
    await doDelete(items.value[0]!);
    expect(deleteAccount).toHaveBeenCalledWith("acc_1", 1);
  });

  it("applies realtime state patches", async () => {
    const { load, items } = useAccountsList();
    await load();
    expect(realtimeHandlers.length).toBeGreaterThan(0);
    realtimeHandlers[0]!({
      topic: "accounts.state",
      data: { id: "acc_1", state: "syncing", lastSyncAt: "2026-10-26T00:00:00Z" },
    });
    expect(items.value[0]!.state).toBe("syncing");
    expect(items.value[0]!.lastSyncAt).toBe("2026-10-26T00:00:00Z");
  });

  it("records load errors", async () => {
    vi.mocked(listAccounts).mockRejectedValueOnce({
      problem: { status: 500, code: "internal_error", detail: "boom" },
    });
    const { load, loadError } = useAccountsList();
    await load();
    expect(loadError.value).toBeTruthy();
  });
});
