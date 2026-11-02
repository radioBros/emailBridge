package connectors

import (
	"fmt"
	"net/http"
	"strings"
)

const (
	TypeSSOAuthentik = "sso-authentik"
	TypeSSOKeycloak  = "sso-keycloak"
	TypeSSOEntra     = "sso-entra"
	TypeSSOGoogle    = "sso-google"
	TypeSSOOkta      = "sso-okta"
)

type ssoPreset struct {
	id, name, icon, color, issuerTpl, issuerPattern, groupClaim, docs string
	scopes                                                            []string
	claimMapping                                                      map[string]string
	extraProps                                                        map[string]any
	required                                                          []string
}

func ssoDef(p ssoPreset) Def {
	props := map[string]any{
		"issuerUrl": map[string]any{
			"type": "string", "format": "uri", "title": "Issuer URL",
			"description": "Template: " + p.issuerPattern,
		},
		"clientId":     map[string]any{"type": "string", "title": "Client ID"},
		"clientSecret": map[string]any{"type": "string", "writeOnly": true, "title": "Client secret"},
		"buttonLabel":  map[string]any{"type": "string", "title": "Login button label", "default": p.name},
		"groupClaim":   map[string]any{"type": "string", "title": "Group claim", "default": p.groupClaim},
		"scopes":       map[string]any{"type": "string", "title": "Scopes (space-separated)", "default": strings.Join(p.scopes, " ")},
	}
	for k, v := range p.extraProps {
		props[k] = v
	}
	req := make([]any, 0, len(p.required))
	for _, r := range p.required {
		req = append(req, r)
	}
	claims := p.claimMapping
	if claims == nil && p.groupClaim != "" {
		claims = map[string]string{"groups": p.groupClaim}
	}
	preset := map[string]any{
		"issuerUrlPattern": p.issuerPattern,
		"scopes":           append([]string(nil), p.scopes...),
		"groupClaim":       p.groupClaim,
		"buttonLabel":      p.name,
		"icon":             p.icon,
		"helpText":         "Register the redirect URI at the IdP after saving the provider: {origin}" + RedirectURIPathTemplate,
		"redirectURIPath":  RedirectURIPathTemplate,
	}
	if claims != nil {
		preset["claimMapping"] = claims
	}
	settingsKey := "integrations." + p.id + "."
	return Def{
		ID: p.id, Version: "1.0.0", Name: p.name, Category: CategorySSO,
		Icon: p.icon, Color: p.color,
		Description:  "OIDC single sign-on preset for " + p.name + ". Use Settings → OIDC “Start from a preset”, or install to seed the defaults.",
		DocsPath:     p.docs,
		ConfigSchema: map[string]any{"type": "object", "required": req, "properties": props, OIDCPresetKey: preset},
		SecretFields: []string{"clientSecret"},
		Capabilities: []string{CapTest},
		Settings: map[string]string{
			settingsKey + "issuerTemplate": p.issuerTpl,
			settingsKey + "groupClaim":     "{{config.groupClaim}}",
			settingsKey + "scopes":         "{{config.scopes}}",
			settingsKey + "buttonLabel":    "{{config.buttonLabel}}",
		},
		TestFunc: HTTPTester(p.name, func(cfg map[string]any, _ map[string]string) (string, string, map[string]string, []byte, error) {
			issuer := strCfg(cfg, "issuerUrl")
			if issuer == "" {
				return "", "", nil, nil, fmt.Errorf("issuerUrl required")
			}
			return http.MethodGet, strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration", nil, nil, nil
		}),
	}
}

// OIDCPresetOf reads the W37 preset extension from a connector definition.
func OIDCPresetOf(d Def) (OIDCPreset, bool) {
	if d.ConfigSchema == nil {
		return OIDCPreset{}, false
	}
	raw, ok := d.ConfigSchema[OIDCPresetKey].(map[string]any)
	if !ok || raw == nil {
		return OIDCPreset{}, false
	}
	out := OIDCPreset{
		IssuerURLPattern: strAny(raw["issuerUrlPattern"]),
		GroupClaim:       strAny(raw["groupClaim"]),
		ButtonLabel:      strAny(raw["buttonLabel"]),
		Icon:             strAny(raw["icon"]),
		HelpText:         strAny(raw["helpText"]),
		RedirectURIPath:  strAny(raw["redirectURIPath"]),
	}
	if scopes, ok := raw["scopes"].([]string); ok {
		out.Scopes = append([]string(nil), scopes...)
	} else if arr, ok := raw["scopes"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok {
				out.Scopes = append(out.Scopes, s)
			}
		}
	}
	if m, ok := raw["claimMapping"].(map[string]string); ok {
		out.ClaimMapping = m
	} else if m, ok := raw["claimMapping"].(map[string]any); ok {
		out.ClaimMapping = map[string]string{}
		for k, v := range m {
			out.ClaimMapping[k] = strAny(v)
		}
	}
	return out, out.IssuerURLPattern != "" || out.ButtonLabel != ""
}

func strAny(v any) string {
	s, _ := v.(string)
	return s
}

// SSOAuthentik returns the Authentik OIDC preset.
func SSOAuthentik() Def {
	return ssoDef(ssoPreset{
		id: TypeSSOAuthentik, name: "Authentik", icon: "mdi-shield-key", color: "#FD4B2D",
		issuerTpl:     "https://{host}/application/o/{app}/",
		issuerPattern: "https://<authentik-host>/application/o/<slug>/", groupClaim: "groups",
		scopes: []string{"openid", "email", "profile"}, docs: "/docs/integrations/sso-authentik",
		claimMapping: map[string]string{"groups": "groups"},
		extraProps:   map[string]any{"host": map[string]any{"type": "string", "title": "Authentik host"}, "app": map[string]any{"type": "string", "title": "Application slug"}},
		required:     []string{"issuerUrl", "clientId", "clientSecret"},
	})
}

// SSOKeycloak returns the Keycloak OIDC preset.
func SSOKeycloak() Def {
	return ssoDef(ssoPreset{
		id: TypeSSOKeycloak, name: "Keycloak", icon: "mdi-key-chain", color: "#4D4D4D",
		issuerTpl:     "https://{host}/realms/{realm}",
		issuerPattern: "https://<keycloak-host>/realms/<realm>", groupClaim: "groups",
		scopes: []string{"openid", "email", "profile"}, docs: "/docs/integrations/sso-keycloak",
		claimMapping: map[string]string{"groups": "groups"},
		extraProps:   map[string]any{"host": map[string]any{"type": "string", "title": "Keycloak host"}, "realm": map[string]any{"type": "string", "title": "Realm", "default": "master"}},
		required:     []string{"issuerUrl", "clientId", "clientSecret"},
	})
}

// SSOEntra returns the Microsoft Entra ID OIDC preset.
func SSOEntra() Def {
	return ssoDef(ssoPreset{
		id: TypeSSOEntra, name: "Microsoft Entra ID", icon: "mdi-microsoft", color: "#00A4EF",
		issuerTpl:     "https://login.microsoftonline.com/{tenant}/v2.0",
		issuerPattern: "https://login.microsoftonline.com/<tenant-id>/v2.0", groupClaim: "groups",
		scopes: []string{"openid", "email", "profile"}, docs: "/docs/integrations/sso-entra",
		claimMapping: map[string]string{"groups": "groups", "roles": "roles"},
		extraProps:   map[string]any{"tenant": map[string]any{"type": "string", "title": "Tenant ID", "default": "common"}},
		required:     []string{"issuerUrl", "clientId", "clientSecret"},
	})
}

// SSOGoogle returns the Google Workspace OIDC preset.
func SSOGoogle() Def {
	return ssoDef(ssoPreset{
		id: TypeSSOGoogle, name: "Google Workspace", icon: "mdi-google", color: "#EA4335",
		issuerTpl:     "https://accounts.google.com",
		issuerPattern: "https://accounts.google.com", groupClaim: "",
		scopes: []string{"openid", "email", "profile"}, docs: "/docs/integrations/sso-google",
		extraProps: map[string]any{
			"allowedEmailDomains": map[string]any{"type": "string", "title": "Allowed email domains (comma-separated)"},
		},
		required: []string{"issuerUrl", "clientId", "clientSecret"},
	})
}

// SSOOkta returns the Okta OIDC preset.
func SSOOkta() Def {
	return ssoDef(ssoPreset{
		id: TypeSSOOkta, name: "Okta", icon: "mdi-shield-lock", color: "#007DC1",
		issuerTpl:     "https://{host}/oauth2/default",
		issuerPattern: "https://<okta-domain>/oauth2/default", groupClaim: "groups",
		scopes: []string{"openid", "email", "profile", "groups"}, docs: "/docs/integrations/sso-okta",
		claimMapping: map[string]string{"groups": "groups"},
		extraProps:   map[string]any{"host": map[string]any{"type": "string", "title": "Okta domain"}},
		required:     []string{"issuerUrl", "clientId", "clientSecret"},
	})
}
