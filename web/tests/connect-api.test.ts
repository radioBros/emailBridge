import { describe, expect, it, vi, beforeEach } from "vitest";
import { ApiProblemError } from "@/api/client";

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
  bootstrapConnect,
  connectErrorCode,
  discoverConnect,
  oauthStartConnect,
  passwordConnect,
  redeemConnectLinkURL,
} from "@/api/connect";

describe("connect api", () => {
  beforeEach(() => {
    vi.mocked(api.get).mockReset();
    vi.mocked(api.post).mockReset();
  });

  it("bootstraps and discovers", async () => {
    vi.mocked(api.get).mockResolvedValue({
      branding: { brandName: "Acme" },
      mode: "hosted",
      csrf: "tok",
      expiresAt: "2026-10-26T00:00:00Z",
    } as never);
    const boot = await bootstrapConnect("lnk_x.secret");
    expect(boot.csrf).toBe("tok");
    expect(api.get).toHaveBeenCalledWith("/public/connect/lnk_x.secret");

    vi.mocked(api.post).mockResolvedValue({
      email: "a@b.c",
      domain: "b.c",
      source: "builtin",
    } as never);
    const d = await discoverConnect("lnk_x.secret", { email: "a@b.c", csrf: "tok" });
    expect(d.source).toBe("builtin");
  });

  it("password and oauth start", async () => {
    vi.mocked(api.post).mockResolvedValueOnce({
      accountId: "acc_1",
      redirectUrl: "https://x/done",
    } as never);
    const done = await passwordConnect("lnk_x.s", {
      email: "a@b.c",
      password: "p",
      csrf: "c",
    });
    expect(done.accountId).toBe("acc_1");

    vi.mocked(api.post).mockResolvedValueOnce({
      authorizeUrl: "https://idp.example/auth",
    } as never);
    const o = await oauthStartConnect("lnk_x.s", {
      email: "a@b.c",
      appId: "oap_1",
      csrf: "c",
    });
    expect(o.authorizeUrl).toContain("idp");
  });

  it("maps problem codes and redeem URL", () => {
    expect(connectErrorCode(new ApiProblemError({ status: 404, code: "connect_link_invalid" }))).toBe(
      "connect_link_invalid",
    );
    expect(connectErrorCode(new Error("x"))).toBe("internal_error");
    expect(redeemConnectLinkURL("lnk_x.secret")).toBe("/v1/connect-links/lnk_x.secret/redeem");
  });
});
