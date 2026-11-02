import { describe, expect, it } from "vitest";
import {
  OIDC_PRESET_KEY,
  parseOidcPreset,
  prefillFromOidcPreset,
} from "@/features/settings/helpers/oidcPreset";

describe("oidc preset helpers", () => {
  const authentik = {
    id: "sso-authentik",
    name: "Authentik",
    icon: "mdi-shield-key",
    color: "#FD4B2D",
    configSchema: {
      type: "object",
      [OIDC_PRESET_KEY]: {
        issuerUrlPattern: "https://<authentik-host>/application/o/<slug>/",
        scopes: ["openid", "email", "profile"],
        groupClaim: "groups",
        buttonLabel: "Authentik",
        icon: "mdi-shield-key",
        claimMapping: { groups: "groups" },
        helpText: "Register the redirect URI at the IdP after saving the provider: {origin}/v1/auth/oidc/<provider-id>/callback",
        redirectURIPath: "/v1/auth/oidc/<provider-id>/callback",
      },
    },
  };

  it("parses catalogue x-eb-oidc-preset", () => {
    const p = parseOidcPreset(authentik);
    expect(p?.issuerUrlPattern).toContain("<authentik-host>");
    expect(p?.scopes).toEqual(["openid", "email", "profile"]);
    expect(p?.groupClaim).toBe("groups");
    expect(p?.buttonLabel).toBe("Authentik");
  });

  it("prefills the OIDC form fields", () => {
    const p = parseOidcPreset(authentik)!;
    const filled = prefillFromOidcPreset(p, "https://gw.example");
    expect(filled.issuerUrl).toBe("https://<authentik-host>/application/o/<slug>/");
    expect(filled.buttonLabel).toBe("Authentik");
    expect(filled.scopesText).toBe("openid, email, profile");
    expect(filled.groupClaim).toBe("groups");
    expect(filled.redirectURI).toBe(
      "https://gw.example/v1/auth/oidc/<provider-id>/callback",
    );
    expect(filled.helpText).toContain("https://gw.example/v1/auth/oidc/<provider-id>/callback");
  });

  it("returns null without preset extension", () => {
    expect(parseOidcPreset({ id: "x", name: "X", configSchema: { type: "object" } })).toBeNull();
  });
});
