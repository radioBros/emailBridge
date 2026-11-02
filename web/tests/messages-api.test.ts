import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn();
const post = vi.fn();
const patch = vi.fn();
const del = vi.fn();
const rawGet = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: (...a: unknown[]) => get(...a),
    post: (...a: unknown[]) => post(...a),
    patch: (...a: unknown[]) => patch(...a),
    delete: (...a: unknown[]) => del(...a),
    raw: { get: (...a: unknown[]) => rawGet(...a) },
  },
  ApiProblemError: class ApiProblemError extends Error {
    problem: { status: number; code: string };
    constructor(problem: { status: number; code: string }) {
      super(problem.code);
      this.problem = problem;
    }
  },
}));

import {
  batchMessages,
  deleteMessage,
  getDraft,
  getMessage,
  getMessageRaw,
  listDrafts,
  listMailboxes,
  listMessages,
  moveMessage,
  patchMessage,
  searchAccounts,
  searchMessages,
} from "@/api/messages";
import { ApiProblemError } from "@/api/client";
import {
  ensureAccountLabels,
  rememberAccounts,
  accountLabelOf,
} from "@/features/messages/helpers/accountCache";

describe("messages api", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists accounts, mailboxes, messages and drafts", async () => {
    get.mockResolvedValueOnce({ data: [{ id: "acc_1", name: "A", email: "a@t" }] });
    await searchAccounts("a");
    expect(get).toHaveBeenCalledWith(expect.stringContaining("/accounts?"));

    get.mockResolvedValueOnce({ data: [] });
    await listMailboxes("acc_1");
    expect(get).toHaveBeenCalledWith(expect.stringContaining("/accounts/acc_1/mailboxes"));

    get.mockResolvedValueOnce({ data: [], total: 0 });
    await listMessages("acc_1", { mailboxId: "mbx_1", page: 2, q: "x" });
    expect(get.mock.calls.at(-1)?.[0]).toContain("mailboxId=mbx_1");

    post.mockResolvedValueOnce({ data: [] });
    await searchMessages("acc_1", { mailboxId: "mbx_1", unseen: true });
    expect(post).toHaveBeenCalledWith(
      expect.stringContaining("/messages/search"),
      expect.objectContaining({ unseen: true }),
    );

    get.mockResolvedValueOnce({ data: [] });
    await listDrafts("acc_1");
    get.mockResolvedValueOnce({ id: "drf_1" });
    await getDraft("acc_1", "drf_1", { text: true });
  });

  it("gets, patches, moves, deletes and batches", async () => {
    get.mockResolvedValueOnce({ id: "msg_1" });
    await getMessage("acc_1", "msg_1", { text: "both", headers: true, envelope: true });
    expect(get.mock.calls.at(-1)?.[0]).toContain("text=both");

    patch.mockResolvedValueOnce({ id: "msg_1" });
    await patchMessage("acc_1", "msg_1", { addFlags: ["\\Seen"] });

    post.mockResolvedValueOnce(undefined);
    await moveMessage("acc_1", "msg_1", { mailboxId: "mbx_2" });

    del.mockResolvedValueOnce(undefined);
    await deleteMessage("acc_1", "msg_1");

    post.mockResolvedValueOnce({ processed: 1 });
    await batchMessages("acc_1", { ids: ["msg_1"], action: "delete" });
  });

  it("fetches raw and maps 404 to missing", async () => {
    rawGet.mockResolvedValueOnce({ data: new ArrayBuffer(4), headers: {} });
    const ok = await getMessageRaw("acc_1", "msg_1");
    expect(ok.ok).toBe(true);

    rawGet.mockRejectedValueOnce(
      new ApiProblemError({ status: 404, code: "not_found" }),
    );
    const missing = await getMessageRaw("acc_1", "msg_1");
    expect(missing).toEqual({ ok: false, missing: true });
  });
});

describe("accountCache", () => {
  it("remembers and resolves labels", async () => {
    rememberAccounts([
      {
        id: "acc_x",
        name: "X",
        email: "x@t",
        organizationId: "o",
        state: "connected",
        createdAt: "",
        updatedAt: "",
        version: 1,
        imap: {} as never,
        smtp: {} as never,
        sync: {} as never,
      },
    ]);
    expect(accountLabelOf("acc_x")).toEqual({ name: "X", email: "x@t" });
    expect(accountLabelOf("acc_missing").name).toBe("acc_missing");
    await ensureAccountLabels(["acc_x"], async () => []);
    await ensureAccountLabels(["acc_y"], async () => [
      {
        id: "acc_y",
        name: "Y",
        email: "y@t",
        organizationId: "o",
        state: "connected",
        createdAt: "",
        updatedAt: "",
        version: 1,
        imap: {} as never,
        smtp: {} as never,
        sync: {} as never,
      },
    ]);
    expect(accountLabelOf("acc_y").name).toBe("Y");
  });
});
