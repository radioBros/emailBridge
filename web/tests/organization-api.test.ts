import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn();
const post = vi.fn();
const rawPatch = vi.fn();
const rawDelete = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: (...args: unknown[]) => get(...args),
    post: (...args: unknown[]) => post(...args),
    raw: {
      patch: (...args: unknown[]) => rawPatch(...args),
      delete: (...args: unknown[]) => rawDelete(...args),
    },
  },
}));

describe("organization + dashboard API modules", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    get.mockResolvedValue({ total: 3, data: [] });
    post.mockResolvedValue({ id: "x" });
    rawPatch.mockResolvedValue({ data: { id: "x", version: 2 } });
    rawDelete.mockResolvedValue({ data: undefined });
  });

  it("counts list totals with page mode", async () => {
    const d = await import("@/api/dashboard");
    await expect(d.countAccounts("connected")).resolves.toBe(3);
    expect(get).toHaveBeenCalledWith(
      expect.stringContaining("/accounts?"),
    );
    expect(String(get.mock.calls[0][0])).toContain("state=connected");
    expect(String(get.mock.calls[0][0])).toContain("includeTotal=true");

    await d.countOutbound("queued");
    await d.countSandboxSince("2026-10-25T00:00:00.000Z");
    await d.countWebhookEndpoints();
    await d.countMembers();
    expect(get).toHaveBeenCalledTimes(5);
  });

  it("returns 0 when total is missing", async () => {
    get.mockResolvedValueOnce({ data: [] });
    const d = await import("@/api/dashboard");
    await expect(d.countList("/members")).resolves.toBe(0);
  });

  it("lists and mutates members with If-Match", async () => {
    const org = await import("@/api/organization");
    await org.listMembers({ page: 2, limit: 10, sort: "email", q: "ada" });
    expect(get.mock.calls.at(-1)?.[0]).toContain("page=2");
    expect(String(get.mock.calls.at(-1)?.[0])).toContain("q=ada");

    await org.inviteMember({ email: "a@b.c", role: "viewer" });
    expect(post).toHaveBeenCalledWith("/members", { email: "a@b.c", role: "viewer" });

    await org.patchMember("mem_1", { role: "admin" }, 3);
    expect(rawPatch).toHaveBeenCalledWith(
      "/members/mem_1",
      { role: "admin" },
      { headers: { "If-Match": '"v3"' } },
    );

    await org.removeMember("mem_1", 3);
    expect(rawDelete).toHaveBeenCalledWith("/members/mem_1", {
      headers: { "If-Match": '"v3"' },
    });
  });

  it("creates, patches and revokes API keys", async () => {
    const org = await import("@/api/organization");
    await org.listApiKeys({ sort: "-createdAt" });
    post.mockResolvedValueOnce({ id: "key_1", token: "secret" });
    await org.createApiKey({ name: "CI", scopes: ["admin"] });
    await org.patchApiKey("key_1", { name: "CI2" }, 1);
    await org.revokeApiKey("key_1", 2);
    expect(rawDelete).toHaveBeenCalledWith("/api-keys/key_1", {
      headers: { "If-Match": '"v2"' },
    });
  });

  it("lists audit log and searches accounts", async () => {
    const org = await import("@/api/organization");
    await org.listAuditLog({
      actorId: "usr_1",
      action: "member.invite",
      from: "2026-10-01T00:00:00Z",
      to: "2026-10-26T00:00:00Z",
    });
    const url = String(get.mock.calls.at(-1)?.[0]);
    expect(url).toContain("actorId=usr_1");
    expect(url).toContain("action=member.invite");

    get.mockResolvedValueOnce({ data: [{ id: "acc_1", name: "A", email: "a@b.c" }] });
    await org.searchAccounts("ada");
    expect(String(get.mock.calls.at(-1)?.[0])).toContain("q=ada");
  });
});
