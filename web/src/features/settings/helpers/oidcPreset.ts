/** local until schema regen — W37 SSO catalogue preset */

export const OIDC_PRESET_KEY = "x-eb-oidc-preset";

export interface OidcCataloguePreset {
  id: string;
  name: string;
  icon?: string;
  color?: string;
  issuerUrlPattern: string;
  scopes: string[];
  groupClaim?: string;
  buttonLabel: string;
  claimMapping?: Record<string, string>;
  helpText?: string;
  redirectURIPath?: string;
}

export interface OidcFormPrefill {
  issuerUrl: string;
  buttonLabel: string;
  scopesText: string;
  groupClaim: string;
  helpText: string;
  redirectURI: string;
}

type CatalogueLike = {
  id: string;
  name: string;
  icon?: string;
  color?: string;
  configSchema?: Record<string, unknown> | null;
};

export const parseOidcPreset = (item: CatalogueLike): OidcCataloguePreset | null => {
  const raw = item.configSchema?.[OIDC_PRESET_KEY];
  if (!raw || typeof raw !== "object") return null;
  const p = raw as Record<string, unknown>;
  const issuerUrlPattern = String(p.issuerUrlPattern ?? "");
  const buttonLabel = String(p.buttonLabel ?? item.name ?? "");
  if (!issuerUrlPattern && !buttonLabel) return null;
  const scopes = Array.isArray(p.scopes)
    ? p.scopes.map((s) => String(s)).filter(Boolean)
    : ["openid", "email", "profile"];
  return {
    id: item.id,
    name: item.name,
    icon: item.icon ?? (typeof p.icon === "string" ? p.icon : undefined),
    color: item.color,
    issuerUrlPattern,
    scopes,
    groupClaim: typeof p.groupClaim === "string" ? p.groupClaim : "",
    buttonLabel,
    claimMapping:
      p.claimMapping && typeof p.claimMapping === "object"
        ? Object.fromEntries(
            Object.entries(p.claimMapping as Record<string, unknown>).map(([k, v]) => [
              k,
              String(v),
            ]),
          )
        : undefined,
    helpText: typeof p.helpText === "string" ? p.helpText : undefined,
    redirectURIPath:
      typeof p.redirectURIPath === "string"
        ? p.redirectURIPath
        : "/v1/auth/oidc/<provider-id>/callback",
  };
};

export const prefillFromOidcPreset = (
  preset: OidcCataloguePreset,
  origin = typeof window !== "undefined" ? window.location.origin : "",
): OidcFormPrefill => {
  const path = preset.redirectURIPath || "/v1/auth/oidc/<provider-id>/callback";
  const redirectURI = `${origin}${path}`;
  const help =
    (preset.helpText || "").replaceAll("{origin}", origin) ||
    `Register this redirect URI at the IdP: ${redirectURI}`;
  return {
    issuerUrl: preset.issuerUrlPattern,
    buttonLabel: preset.buttonLabel,
    scopesText: preset.scopes.join(", "),
    groupClaim: preset.groupClaim ?? "",
    helpText: help,
    redirectURI,
  };
};
