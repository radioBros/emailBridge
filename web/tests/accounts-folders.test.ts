import { beforeEach, describe, expect, it, vi } from "vitest";
import { ref } from "vue";

vi.mock("vue-i18n", async (importOriginal) => {
  const actual = await importOriginal<typeof import("vue-i18n")>();
  return {
    ...actual,
    useI18n: () => ({ t: (k: string) => k, te: () => true }),
  };
});

vi.mock("@/api/messages", () => ({
  listMailboxes: vi.fn(async () => ({
    data: [
      {
        id: "mbx_inbox",
        accountId: "acc_1",
        path: "INBOX",
        delimiter: "/",
        specialUse: "\\Inbox",
        uidValidity: 1,
        exists: 12,
        status: "ok",
        syncMode: "idle",
        lastSyncAt: "2026-10-26T10:00:00Z",
      },
      {
        id: "mbx_sent",
        accountId: "acc_1",
        path: "INBOX/Sent",
        delimiter: "/",
        specialUse: "\\Sent",
        uidValidity: 1,
        exists: 3,
        status: "syncing",
      },
    ],
    nextCursor: null,
  })),
}));

import { listMailboxes } from "@/api/messages";
import { useAccountFolders } from "@/features/accounts/composables/useAccountFolders";

describe("useAccountFolders", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("loads mailboxes as a depth-ordered tree", async () => {
    const accountId = ref("acc_1");
    const { load, nodes, empty } = useAccountFolders(accountId);
    await load();
    expect(listMailboxes).toHaveBeenCalledWith("acc_1");
    expect(nodes.value).toHaveLength(2);
    expect(nodes.value[0]?.mailbox.path).toBe("INBOX");
    expect(nodes.value[0]?.depth).toBe(0);
    expect(nodes.value[1]?.mailbox.path).toBe("INBOX/Sent");
    expect(nodes.value[1]?.depth).toBe(1);
    expect(empty.value).toBe(false);
  });

  it("reports empty when the account has no folders", async () => {
    vi.mocked(listMailboxes).mockResolvedValueOnce({ data: [], nextCursor: null });
    const { load, empty, nodes } = useAccountFolders(ref("acc_2"));
    await load();
    expect(nodes.value).toHaveLength(0);
    expect(empty.value).toBe(true);
  });
});
