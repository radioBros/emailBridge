import { beforeEach, describe, expect, it, vi } from "vitest";

const get = vi.fn();
const post = vi.fn();

vi.mock("@/api/client", () => ({
  api: {
    get: (...a: unknown[]) => get(...a),
    post: (...a: unknown[]) => post(...a),
  },
}));

import {
  checkAccountHealth,
  getAccountHealth,
  listAccountHealth,
} from "@/api/health";

describe("health api", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("gets account health", async () => {
    get.mockResolvedValue({ accountId: "acc_1", severity: "ok" });
    await getAccountHealth("acc_1");
    expect(get).toHaveBeenCalledWith("/accounts/acc_1/health");
  });

  it("runs live check", async () => {
    post.mockResolvedValue({ accountId: "acc_1", liveCheck: { ok: true } });
    await checkAccountHealth("acc_1");
    expect(post).toHaveBeenCalledWith("/accounts/acc_1/health/check");
  });

  it("lists org health with query", async () => {
    get.mockResolvedValue({
      counts: { ok: 1, info: 0, warning: 0, critical: 0, total: 1 },
      data: [],
      page: 1,
      limit: 25,
    });
    await listAccountHealth({
      page: 2,
      limit: 10,
      includeTotal: true,
      sort: "-severity",
      severity: "critical",
      q: "ada",
    });
    const url = get.mock.calls[0][0] as string;
    expect(url).toContain("/health/accounts?");
    expect(url).toContain("page=2");
    expect(url).toContain("limit=10");
    expect(url).toContain("includeTotal=true");
    expect(url).toContain("sort=-severity");
    expect(url).toContain("severity=critical");
    expect(url).toContain("q=ada");
  });
});
