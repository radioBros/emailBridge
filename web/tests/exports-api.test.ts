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
  cancelExport,
  createExport,
  exportDownloadPath,
  getExport,
  listExports,
  signedExportURL,
} from "@/api/exports";

describe("exports api", () => {
  beforeEach(() => {
    get.mockReset();
    post.mockReset();
  });

  it("lists with query params", async () => {
    get.mockResolvedValue({ items: [], total: 0 });
    await listExports("acc_01", { page: 2, limit: 10, includeTotal: true, sort: "-createdAt" });
    expect(get).toHaveBeenCalledWith(
      expect.stringMatching(/\/accounts\/acc_01\/exports\?/),
    );
    const url = get.mock.calls[0][0] as string;
    expect(url).toContain("page=2");
    expect(url).toContain("limit=10");
    expect(url).toContain("includeTotal=true");
    expect(url).toContain("sort=-createdAt");
  });

  it("gets, creates, cancels, signed url, download path", async () => {
    get.mockResolvedValue({ id: "exp_1", state: "succeeded" });
    await getExport("acc_01", "exp_1");
    expect(get).toHaveBeenCalledWith("/accounts/acc_01/exports/exp_1");

    post.mockResolvedValue({ export: { id: "exp_2" }, job: {} });
    await createExport("acc_01", { include: { metadataOnly: true } });
    expect(post).toHaveBeenCalledWith("/accounts/acc_01/exports", {
      include: { metadataOnly: true },
    });

    post.mockResolvedValue({ id: "exp_1", state: "cancelled" });
    await cancelExport("acc_01", "exp_1");
    expect(post).toHaveBeenCalledWith("/accounts/acc_01/exports/exp_1/cancel");

    get.mockResolvedValue({ url: "http://x/e/t", expiresAt: "2026-01-01T00:00:00Z" });
    await signedExportURL("acc_01", "exp_1");
    expect(get).toHaveBeenCalledWith(
      "/accounts/acc_01/exports/exp_1/download?url=signed",
    );

    expect(exportDownloadPath("acc_01", "exp_1")).toBe(
      "/v1/accounts/acc_01/exports/exp_1/download",
    );
  });
});
