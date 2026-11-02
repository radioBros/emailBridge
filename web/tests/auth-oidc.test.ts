import { afterEach, describe, expect, it, vi } from "vitest";
import {
  listPublicOidcProviders,
  oidcStartUrl,
  listOidcProviders,
  createOidcProvider,
  deleteOidcProvider,
} from "@/api/oidc";

vi.mock("@/api/client", () => {
  const api = {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  };
  return { api };
});

import { api } from "@/api/client";

describe("oidc api", () => {
  afterEach(() => {
    vi.clearAllMocks();
  });

  it("lists public providers", async () => {
    vi.mocked(api.get).mockResolvedValue({
      data: [{ id: "oidc_1", buttonLabel: "Authentik" }],
    } as never);
    await expect(listPublicOidcProviders()).resolves.toEqual([
      { id: "oidc_1", buttonLabel: "Authentik" },
    ]);
    expect(api.get).toHaveBeenCalledWith("/auth/oidc/providers");
  });

  it("builds start url with redirect", () => {
    expect(oidcStartUrl("oidc_abc", "/")).toBe(
      "/v1/auth/oidc/oidc_abc/start?redirect_uri=%2F",
    );
    expect(oidcStartUrl("oidc_abc")).toBe("/v1/auth/oidc/oidc_abc/start");
  });

  it("admin CRUD helpers", async () => {
    vi.mocked(api.get).mockResolvedValue({ data: [] } as never);
    vi.mocked(api.post).mockResolvedValue({
      id: "oidc_1",
      buttonLabel: "SSO",
      autoProvision: false,
      enabled: true,
    } as never);
    vi.mocked(api.delete).mockResolvedValue(undefined as never);

    await expect(listOidcProviders()).resolves.toEqual([]);
    await createOidcProvider({
      issuerUrl: "https://idp.example",
      clientId: "c",
      clientSecret: "s",
      buttonLabel: "SSO",
    });
    await deleteOidcProvider("oidc_1");
    expect(api.post).toHaveBeenCalledWith(
      "/oidc-providers",
      expect.objectContaining({ buttonLabel: "SSO" }),
    );
    expect(api.delete).toHaveBeenCalledWith("/oidc-providers/oidc_1");
  });
});
