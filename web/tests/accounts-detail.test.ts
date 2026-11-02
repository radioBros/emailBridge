import { beforeEach, describe, expect, it, vi } from "vitest";
import { nextTick } from "vue";
import { createPinia, setActivePinia } from "pinia";

const { replace } = vi.hoisted(() => ({ replace: vi.fn() }));

vi.mock("vue-router", () => ({
  useRoute: () => ({ params: { id: "acc_1" }, query: {} }),
  useRouter: () => ({ push: vi.fn(), replace }),
}));

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

vi.mock("@/composables/useRealtime", () => ({
  useRealtime: () => ({ status: { value: "idle" }, lastMessage: { value: null } }),
}));

const account = {
  id: "acc_1",
  organizationId: "org_1",
  name: "A",
  email: "a@b.co",
  state: "connected",
  imap: {
    host: "h",
    port: 993,
    tls: "implicit",
    auth: { user: "u", hasPassword: true },
  },
  smtp: {
    host: "h",
    port: 465,
    tls: "implicit",
    policy: "standard",
    auth: { user: "u", hasPassword: true },
  },
  sync: { paths: ["*"], idlePaths: ["INBOX"] },
  switches: { "protocolLogs.enabled": false },
  createdAt: "2026-01-01T00:00:00Z",
  updatedAt: "2026-01-01T00:00:00Z",
  version: 1,
  stateReason: {
    at: "2026-01-02T00:00:00Z",
    code: "x",
    kind: "connect",
    message: "boom",
  },
};

vi.mock("@/api/accounts", () => ({
  getAccount: vi.fn(async () => account),
  listAccountOutbound: vi.fn(async () => ({
    data: [{ id: "o1", state: "queued", envelope: { from: "a", to: ["b"] }, maxAttempts: 8 }],
    nextCursor: null,
    total: 1,
  })),
  listProtocolLogs: vi.fn(async () => ({
    data: [{ id: "pl1", dir: "C", line: "x", proto: "imap", time: 1 }],
    nextCursor: "c2",
  })),
  downloadProtocolLogs: vi.fn(async () => new Blob(["x"])),
  patchAccount: vi.fn(async (_id: string, body: { switches?: Record<string, unknown> }) => ({
    ...account,
    switches: body.switches,
    version: 2,
  })),
  syncAccount: vi.fn(async () => ({ job: { id: "j" } })),
  reconnectAccount: vi.fn(async () => ({ job: { id: "j" } })),
  resumeAccount: vi.fn(async () => account),
  deleteAccount: vi.fn(async () => ({ job: { id: "j" } })),
}));

import {
  deleteAccount,
  downloadProtocolLogs,
  getAccount,
  listAccountOutbound,
  listProtocolLogs,
  patchAccount,
  reconnectAccount,
  resumeAccount,
  syncAccount,
} from "@/api/accounts";
import { useAccountDetail } from "@/features/accounts/composables/useAccountDetail";

describe("useAccountDetail", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
  });

  it("loads account and recent errors", async () => {
    const { load, account: acc, recentErrors, isSwitchOn } = useAccountDetail();
    await load();
    expect(getAccount).toHaveBeenCalledWith("acc_1");
    expect(acc.value?.name).toBe("A");
    expect(recentErrors.value).toHaveLength(1);
    expect(isSwitchOn("protocolLogs.enabled")).toBe(false);
  });

  it("syncs reconnects resumes deletes and toggles a switch", async () => {
    const { load, doSync, doReconnect, doResume, doDelete, setSwitch } = useAccountDetail();
    await load();
    await doSync("full");
    expect(syncAccount).toHaveBeenCalledWith("acc_1", { mode: "full" });
    await doReconnect();
    expect(reconnectAccount).toHaveBeenCalledWith("acc_1");
    await doResume();
    expect(resumeAccount).toHaveBeenCalledWith("acc_1");
    await setSwitch("protocolLogs.enabled", true);
    expect(patchAccount).toHaveBeenCalled();
    await doDelete();
    expect(deleteAccount).toHaveBeenCalled();
  });

  it("loads outbox logs and download", async () => {
    const { load, loadOutbox, loadLogs, downloadLogs, outboxItems, logItems, logCursor } =
      useAccountDetail();
    await load();
    await loadOutbox();
    expect(listAccountOutbound).toHaveBeenCalled();
    expect(outboxItems.value).toHaveLength(1);
    await loadLogs();
    expect(listProtocolLogs).toHaveBeenCalled();
    expect(logItems.value).toHaveLength(1);
    expect(logCursor.value).toBe("c2");
    await loadLogs(true);
    expect(logItems.value.length).toBeGreaterThanOrEqual(1);

    const createEl = document.createElement.bind(document);
    const click = vi.fn();
    vi.spyOn(document, "createElement").mockImplementation((tag: string) => {
      const el = createEl(tag);
      if (tag === "a") (el as HTMLAnchorElement).click = click;
      return el;
    });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:x");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    await downloadLogs();
    expect(downloadProtocolLogs).toHaveBeenCalled();
    expect(click).toHaveBeenCalled();
  });

  it("keeps the active tab in the URL query", async () => {
    const { tab } = useAccountDetail();
    expect(tab.value).toBe("overview");
    tab.value = "folders";
    await nextTick();
    expect(replace).toHaveBeenCalledWith({ query: { tab: "folders" } });
    tab.value = "events";
    await nextTick();
    expect(replace).toHaveBeenCalledWith({ query: { tab: "events" } });
    tab.value = "routing";
    await nextTick();
    expect(replace).toHaveBeenCalledWith({ query: { tab: "routing" } });
  });
});

