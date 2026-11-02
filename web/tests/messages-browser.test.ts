import { beforeEach, describe, expect, it, vi } from "vitest";
import { createPinia, setActivePinia } from "pinia";

vi.mock("vue-router", () => ({
  useRoute: () => ({ query: {} }),
  useRouter: () => ({ replace: vi.fn() }),
}));

vi.mock("@/composables/useProblem", () => ({
  useProblem: () => ({
    messageOf: (err: unknown) => (err instanceof Error ? err.message : String(err)),
    fieldErrors: () => ({}),
  }),
}));

const listMailboxes = vi.fn();
const listMessages = vi.fn();
const searchMessages = vi.fn();
const searchAccounts = vi.fn();
const batchMessages = vi.fn();

vi.mock("@/api/messages", () => ({
  listMailboxes: (...a: unknown[]) => listMailboxes(...a),
  listMessages: (...a: unknown[]) => listMessages(...a),
  searchMessages: (...a: unknown[]) => searchMessages(...a),
  searchAccounts: (...a: unknown[]) => searchAccounts(...a),
  batchMessages: (...a: unknown[]) => batchMessages(...a),
}));

import { useMessagesBrowser } from "@/features/messages/composables/useMessagesBrowser";
import { FLAG_SEEN } from "@/features/messages/helpers/flags";

describe("useMessagesBrowser", () => {
  beforeEach(() => {
    setActivePinia(createPinia());
    vi.clearAllMocks();
    searchAccounts.mockResolvedValue([
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
    ]);
    listMailboxes.mockResolvedValue({
      data: [
        {
          id: "mbx_1",
          accountId: "acc_1",
          path: "INBOX",
          specialUse: "\\Inbox",
          uidValidity: 1,
          exists: 2,
          status: "active",
        },
      ],
    });
    listMessages.mockResolvedValue({
      data: [
        {
          id: "msg_1",
          accountId: "acc_1",
          mailboxId: "mbx_1",
          uidValidity: 1,
          uid: 1,
          flags: [],
          emailId: "e1",
          internalDate: "2026-10-26T10:00:00Z",
          size: 100,
        },
      ],
      total: 1,
    });
    searchMessages.mockResolvedValue({ data: [] });
    batchMessages.mockResolvedValue({ processed: 1 });
  });

  it("loads accounts, folders and messages", async () => {
    const b = useMessagesBrowser();
    await b.loadAccounts();
    expect(searchAccounts).toHaveBeenCalled();
    expect(b.accountOptions.value).toHaveLength(1);

    await b.selectAccount("acc_1");
    expect(listMailboxes).toHaveBeenCalledWith("acc_1");
    expect(b.folderNodes.value).toHaveLength(1);

    b.selectFolder("mbx_1");
    await Promise.resolve();
    await vi.waitFor(() => expect(listMessages).toHaveBeenCalled());
    expect(b.items.value).toHaveLength(1);
    expect(b.empty.value).toBe(false);
  });

  it("uses search for unread filter and runs bulk mark read", async () => {
    const b = useMessagesBrowser();
    await b.selectAccount("acc_1");
    b.selectFolder("mbx_1");
    await vi.waitFor(() => expect(b.items.value.length).toBe(1));

    b.flagFilter.value = "unread";
    await vi.waitFor(() => expect(searchMessages).toHaveBeenCalled());

    b.selected.value = ["msg_1"];
    const ok = await b.bulkMarkRead();
    expect(ok).toBe(true);
    expect(batchMessages).toHaveBeenCalledWith(
      "acc_1",
      expect.objectContaining({
        ids: ["msg_1"],
        action: "flags",
        addFlags: [FLAG_SEEN],
      }),
    );
  });

  it("runs bulk flag/unflag/delete/move and navigation helpers", async () => {
    const b = useMessagesBrowser();
    await b.selectAccount("acc_1");
    b.selectFolder("mbx_1");
    await vi.waitFor(() => expect(b.items.value.length).toBe(1));
    b.selected.value = ["msg_1"];
    expect(await b.bulkFlag()).toBe(true);
    b.selected.value = ["msg_1"];
    expect(await b.bulkUnflag()).toBe(true);
    b.selected.value = ["msg_1"];
    expect(await b.bulkMarkUnread()).toBe(true);
    b.selected.value = ["msg_1"];
    expect(await b.bulkMove("mbx_2")).toBe(true);
    b.selected.value = ["msg_1"];
    expect(await b.bulkDelete()).toBe(true);
    b.openMessage("msg_1");
    expect(b.mobileStep.value).toBe("reader");
    b.clearMessage();
    expect(b.mobileStep.value).toBe("list");
    b.backToFolders();
    expect(b.mobileStep.value).toBe("folders");
  });
});
