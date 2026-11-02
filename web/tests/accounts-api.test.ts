import { beforeEach, describe, expect, it, vi } from "vitest";

const { get, post, raw } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  raw: {
    patch: vi.fn(),
    delete: vi.fn(),
    get: vi.fn(),
  },
}));

vi.mock("@/api/client", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
    raw,
  },
}));

import {
  createAccount,
  deleteAccount,
  downloadProtocolLogs,
  getAccount,
  listAccountOutbound,
  listAccounts,
  listProtocolLogs,
  patchAccount,
  reconnectAccount,
  resumeAccount,
  syncAccount,
  verifyAccountSettings,
} from "@/api/accounts";

describe("accounts api module", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists with query params", async () => {
    get.mockResolvedValueOnce({ data: [], nextCursor: null, total: 0 });
    await listAccounts({ page: 2, limit: 10, state: "connected", q: "ada" });
    expect(get).toHaveBeenCalledWith(expect.stringContaining("/accounts?"));
    const url = String(get.mock.calls[0]![0]);
    expect(url).toContain("page=2");
    expect(url).toContain("state=connected");
    expect(url).toContain("q=ada");
  });

  it("gets one account", async () => {
    get.mockResolvedValueOnce({ id: "acc_1" });
    await getAccount("acc_1");
    expect(get).toHaveBeenCalledWith("/accounts/acc_1");
  });

  it("creates and verifies", async () => {
    post.mockResolvedValueOnce({ id: "acc_1" });
    await createAccount({
      name: "A",
      email: "a@b.co",
      imap: { host: "h", port: 993, tls: "implicit", auth: { user: "u", pass: "p" } },
      smtp: { host: "h", port: 465, tls: "implicit", auth: { user: "u", pass: "p" } },
    });
    expect(post).toHaveBeenCalledWith("/accounts", expect.any(Object));

    post.mockResolvedValueOnce({ imap: { ok: true }, smtp: { ok: true } });
    await verifyAccountSettings({ ignoreCertErrors: false });
    expect(post).toHaveBeenCalledWith("/accounts/verify", expect.any(Object));
  });

  it("patches with If-Match", async () => {
    raw.patch.mockResolvedValueOnce({ data: { id: "acc_1", version: 2 } });
    await patchAccount("acc_1", { name: "B" }, 1);
    expect(raw.patch).toHaveBeenCalledWith(
      "/accounts/acc_1",
      { name: "B" },
      { headers: { "If-Match": '"v1"' } },
    );
  });

  it("deletes reconnects resumes and syncs", async () => {
    raw.delete.mockResolvedValueOnce({ data: { job: { id: "j" } } });
    await deleteAccount("acc_1", 2);
    expect(raw.delete).toHaveBeenCalledWith("/accounts/acc_1", {
      headers: { "If-Match": '"v2"' },
    });

    post.mockResolvedValueOnce({ job: { id: "j" } });
    await syncAccount("acc_1", { mode: "full" });
    expect(post).toHaveBeenCalledWith("/accounts/acc_1/sync", { mode: "full" });

    post.mockResolvedValueOnce({ job: { id: "j" } });
    await reconnectAccount("acc_1");
    expect(post).toHaveBeenCalledWith("/accounts/acc_1/reconnect");

    post.mockResolvedValueOnce({ id: "acc_1", state: "connecting" });
    await resumeAccount("acc_1");
    expect(post).toHaveBeenCalledWith("/accounts/acc_1/resume");
  });

  it("lists protocol logs, downloads and outbox", async () => {
    get.mockResolvedValueOnce({ data: [], nextCursor: null });
    await listProtocolLogs("acc_1", { proto: "imap", from: "2026-01-01" });
    expect(get).toHaveBeenCalledWith(expect.stringContaining("/accounts/acc_1/protocol-logs?"));

    raw.get.mockResolvedValueOnce({ data: new Blob(["x"]) });
    await downloadProtocolLogs("acc_1", { proto: "smtp" });
    expect(raw.get).toHaveBeenCalledWith(
      expect.stringContaining("/accounts/acc_1/protocol-logs/download?"),
      { responseType: "blob" },
    );

    get.mockResolvedValueOnce({ data: [], nextCursor: null, total: 0 });
    await listAccountOutbound("acc_1", { page: 1, state: "queued" });
    expect(get).toHaveBeenCalledWith(expect.stringContaining("/outbound-messages?"));
  });
});
