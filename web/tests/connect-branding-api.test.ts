import { describe, expect, it, vi, beforeEach } from "vitest";

vi.mock("@/api/client", async () => {
  const actual = await vi.importActual<typeof import("@/api/client")>("@/api/client");
  return {
    ...actual,
    api: {
      get: vi.fn(),
      post: vi.fn(),
      raw: {
        get: vi.fn(),
        patch: vi.fn(),
      },
    },
  };
});

import { api } from "@/api/client";
import {
  getConnectBranding,
  patchConnectBranding,
  previewConnectBranding,
} from "@/api/connect";

describe("connect branding api", () => {
  beforeEach(() => {
    vi.mocked(api.post).mockReset();
    vi.mocked(api.raw.get).mockReset();
    vi.mocked(api.raw.patch).mockReset();
  });

  it("loads and patches branding", async () => {
    vi.mocked(api.raw.get).mockResolvedValue({
      data: { brandName: "Acme", headerHtml: "<p>Hi</p>" },
      headers: { etag: '"3"' },
    } as never);
    const got = await getConnectBranding();
    expect(got.data.brandName).toBe("Acme");
    expect(got.etag).toBe('"3"');

    vi.mocked(api.raw.patch).mockResolvedValue({
      data: { brandName: "Acme", headerHtml: "<p>Hi</p>" },
      headers: { etag: '"4"' },
    } as never);
    const patched = await patchConnectBranding({ headerHtml: "<p>Hi</p>" }, '"3"');
    expect(patched.etag).toBe('"4"');
    expect(api.raw.patch).toHaveBeenCalledWith(
      "/connect/branding",
      { headerHtml: "<p>Hi</p>" },
      { headers: { "If-Match": '"3"' } },
    );
  });

  it("previews sanitised html", async () => {
    vi.mocked(api.post).mockResolvedValue({
      headerHtml: "<p>safe</p>",
      headHtml: "<meta name=\"description\" content=\"x\">",
    } as never);
    const prev = await previewConnectBranding({
      headerHtml: "<p>safe</p><script>x</script>",
    });
    expect(prev.headerHtml).toContain("safe");
    expect(api.post).toHaveBeenCalledWith("/connect/branding/preview", {
      headerHtml: "<p>safe</p><script>x</script>",
    });
  });
});
