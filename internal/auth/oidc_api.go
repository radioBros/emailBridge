package auth

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func (s *Service) registerOIDCAdmin(srv *httpapi.Server) {
	ext := httpapi.Phase("P3")
	who := " Instance owners only (`instance.admin`)."
	httpapi.Register(srv, huma.Operation{
		OperationID: "listOidcProviders", Method: http.MethodGet, Path: "/v1/oidc-providers",
		Summary: "List OIDC providers", Tags: []string{"oidc"},
		Description: "Lists the OpenID Connect providers (Authentik, Keycloak, Entra ID, Google …) admins can sign in " +
			"with, including disabled ones. Client secrets are stored encrypted and never returned." + who +
			" The login page uses the public `GET /v1/auth/oidc/providers` instead. Not paginated (a handful of providers).",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden),
		Extensions: httpapi.WithScope(ext, "instance.admin"),
	}, s.adminListOIDC)
	httpapi.Register(srv, huma.Operation{
		OperationID: "createOidcProvider", Method: http.MethodPost, Path: "/v1/oidc-providers",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create OIDC provider", Tags: []string{"oidc"},
		Description: "Adds an OpenID Connect provider. Register `{EB_PUBLIC_URL}/v1/auth/oidc/{id}/callback` as redirect " +
			"URI at the provider (the ID is in the response). The client secret is encrypted at rest. " +
			"`groupRoles` maps provider groups (from `groupClaim`) to organisation roles granted at each sign-in." + who +
			" The same issuer + client ID twice gives `409 conflict`. Enabled by default.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeValidationFailed, errs.CodeConflict),
		Extensions: httpapi.WithScope(ext, "instance.admin"),
	}, s.adminCreateOIDC)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getOidcProvider", Method: http.MethodGet, Path: "/v1/oidc-providers/{providerId}",
		Summary: "Get OIDC provider", Tags: []string{"oidc"},
		Description: "Returns one OIDC provider (without its client secret)." + who,
		Metadata:    httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound),
		Extensions:  httpapi.WithScope(ext, "instance.admin"),
	}, s.adminGetOIDC)
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchOidcProvider", Method: http.MethodPatch, Path: "/v1/oidc-providers/{providerId}",
		Summary: "Update OIDC provider", Tags: []string{"oidc"},
		Description: "Changes any field of an OIDC provider; omitted fields stay unchanged, `clientSecret` is replaced " +
			"only when sent (and cannot be blank). Disable a provider with `enabled: false` to hide it from the " +
			"login page without deleting it. Existing sessions are not affected." + who,
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound, errs.CodeValidationFailed,
			errs.CodeConflict),
		Extensions: httpapi.WithScope(ext, "instance.admin"),
	}, s.adminPatchOIDC)
	httpapi.Register(srv, huma.Operation{
		OperationID: "deleteOidcProvider", Method: http.MethodDelete, Path: "/v1/oidc-providers/{providerId}",
		Summary: "Delete OIDC provider", Tags: []string{"oidc"},
		Description: "Removes an OIDC provider; its button disappears from the login page and pending sign-ins through " +
			"it fail. Users created through it keep their EmailBridge profile and can still sign in by other " +
			"methods. Existing sessions are not revoked." + who + " Answers `204`.",
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeNotFound),
		Extensions: httpapi.WithScope(ext, "instance.admin"),
	}, s.adminDeleteOIDC)
}

type oidcListOut struct {
	Body struct {
		Data []OIDCProvider `json:"data" doc:"Every configured provider."`
	}
}

func (s *Service) adminListOIDC(ctx context.Context, _ *struct{}) (*oidcListOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
		return nil, err
	}
	list, err := s.listOIDCProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := &oidcListOut{}
	out.Body.Data = list
	return out, nil
}

type oidcCreateBody struct {
	IssuerURL           string            `json:"issuerUrl" doc:"Issuer URL (required); a trailing slash is removed." example:"https://auth.example.com/application/o/emailbridge"`
	ClientID            string            `json:"clientId" doc:"OAuth client ID (required)." example:"emailbridge-admin"`
	ClientSecret        string            `json:"clientSecret" doc:"OAuth client secret (required, write-only, stored encrypted)." example:"9vQe2Lk7Rz4TfX1bN8mW3pY6sD0hJ5gA"`
	Scopes              []string          `json:"scopes,omitempty" doc:"Scopes to request (default openid, email, profile)." example:"[\"openid\",\"email\",\"profile\",\"groups\"]"`
	ButtonLabel         string            `json:"buttonLabel" doc:"Login-page button label (required)." example:"Sign in with Authentik"`
	AllowedEmailDomains []string          `json:"allowedEmailDomains,omitempty" doc:"Restrict sign-in to verified emails in these domains." example:"[\"example.com\"]"`
	GroupClaim          string            `json:"groupClaim,omitempty" doc:"ID-token claim with the user's groups." example:"groups"`
	GroupRoles          map[string]string `json:"groupRoles,omitempty" doc:"Group → org_<id>:<role> (owner, admin, operator or viewer)." example:"{\"emailbridge-admins\":\"org_01m5caqpk0kast63yc13wpy2d9:admin\"}"`
	AutoProvision       bool              `json:"autoProvision" doc:"Create unknown users on first sign-in (otherwise only existing users and pending invitees)." example:"false"`
	Enabled             *bool             `json:"enabled,omitempty" doc:"Offer the provider on the login page (default true)." example:"true"`
}

type oidcCreateIn struct {
	Body oidcCreateBody
}

type oidcOneOut struct {
	Body OIDCProvider
}

func (s *Service) adminCreateOIDC(ctx context.Context, in *oidcCreateIn) (*oidcOneOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
		return nil, err
	}
	p, err := s.createOIDCProvider(ctx, oidcCreateInput{
		IssuerURL: in.Body.IssuerURL, ClientID: in.Body.ClientID, ClientSecret: in.Body.ClientSecret,
		Scopes: in.Body.Scopes, ButtonLabel: in.Body.ButtonLabel, AllowedEmailDomains: in.Body.AllowedEmailDomains,
		GroupClaim: in.Body.GroupClaim, GroupRoles: in.Body.GroupRoles, AutoProvision: in.Body.AutoProvision,
		Enabled: in.Body.Enabled,
	})
	if err != nil {
		return nil, err
	}
	changes := audit.Diff(nil, p)
	if changes == nil {
		changes = audit.Changes{}
	}
	changes["clientSecret"] = audit.FieldChange{After: audit.Secret("set")}
	s.auditProviderAdmin(ctx, "oidc.provider.created", p.ID, changes)
	return &oidcOneOut{Body: *p}, nil
}

// auditProviderAdmin records an OIDC provider change. Providers are instance-wide, so the record carries no
// organisation (only instance owners see it); the client secret is only ever "[redacted]".
func (s *Service) auditProviderAdmin(ctx context.Context, action, providerID string, changes audit.Changes) {
	if s.Audit == nil {
		return
	}
	if c, ok := tenancy.CallerFrom(ctx); ok {
		c.OrganizationID = ""
		ctx = tenancy.WithCaller(ctx, c)
	}
	if err := s.Audit.Record(ctx, action, audit.Target{Type: "oidcProvider", ID: providerID}, changes); err != nil && s.log != nil {
		s.log.WarnContext(ctx, "oidc provider audit failed", "err", err, "action", action, "providerId", providerID)
	}
}

type oidcIDIn struct {
	ProviderID string `path:"providerId" doc:"Provider ID (oidc_…)." example:"oidc_01m5d68e607knapbtf5smmk5re"`
}

func (s *Service) adminGetOIDC(ctx context.Context, in *oidcIDIn) (*oidcOneOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
		return nil, err
	}
	p, err := s.getOIDCProvider(ctx, in.ProviderID)
	if err != nil {
		return nil, err
	}
	return &oidcOneOut{Body: *p}, nil
}

type oidcPatchBody struct {
	IssuerURL           *string            `json:"issuerUrl,omitempty" doc:"New issuer URL (not blank)." example:"https://auth.example.com/application/o/emailbridge"`
	ClientID            *string            `json:"clientId,omitempty" doc:"New client ID (not blank)." example:"emailbridge-admin"`
	ClientSecret        *string            `json:"clientSecret,omitempty" doc:"New client secret (write-only, not blank)." example:"Xa8Kq2Wn5Rt9Lm3Vb7Jc1Hd4Fg6Sz0Py"`
	Scopes              *[]string          `json:"scopes,omitempty" doc:"Replaces the scopes (empty list = defaults)." example:"[\"openid\",\"email\",\"profile\"]"`
	ButtonLabel         *string            `json:"buttonLabel,omitempty" doc:"New button label (not blank)." example:"Company SSO"`
	AllowedEmailDomains *[]string          `json:"allowedEmailDomains,omitempty" doc:"Replaces the allowed domains (empty list = any)." example:"[\"example.com\",\"example.org\"]"`
	GroupClaim          *string            `json:"groupClaim,omitempty" doc:"New group claim name (empty disables group mapping)." example:"groups"`
	GroupRoles          *map[string]string `json:"groupRoles,omitempty" doc:"Replaces the group → org_<id>:<role> map." example:"{\"support\":\"org_01m5caqpk0kast63yc13wpy2d9:operator\"}"`
	AutoProvision       *bool              `json:"autoProvision,omitempty" doc:"Create unknown users on first sign-in." example:"true"`
	Enabled             *bool              `json:"enabled,omitempty" doc:"Offer the provider on the login page." example:"false"`
}

type oidcPatchIn struct {
	ProviderID string `path:"providerId" doc:"Provider ID (oidc_…)." example:"oidc_01m5d68e607knapbtf5smmk5re"`
	Body       oidcPatchBody
}

func (s *Service) adminPatchOIDC(ctx context.Context, in *oidcPatchIn) (*oidcOneOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
		return nil, err
	}
	before, err := s.getOIDCProvider(ctx, in.ProviderID)
	if err != nil {
		return nil, err
	}
	p, err := s.patchOIDCProvider(ctx, in.ProviderID, oidcPatchInput{
		IssuerURL: in.Body.IssuerURL, ClientID: in.Body.ClientID, ClientSecret: in.Body.ClientSecret,
		Scopes: in.Body.Scopes, ButtonLabel: in.Body.ButtonLabel, AllowedEmailDomains: in.Body.AllowedEmailDomains,
		GroupClaim: in.Body.GroupClaim, GroupRoles: in.Body.GroupRoles, AutoProvision: in.Body.AutoProvision,
		Enabled: in.Body.Enabled,
	})
	if err != nil {
		return nil, err
	}
	changes := audit.Diff(before, p)
	delete(changes, "updatedAt")
	if in.Body.ClientSecret != nil {
		if changes == nil {
			changes = audit.Changes{}
		}
		changes["clientSecret"] = audit.FieldChange{Before: audit.Secret("set"), After: audit.Secret("set")}
	}
	s.auditProviderAdmin(ctx, "oidc.provider.updated", p.ID, changes)
	return &oidcOneOut{Body: *p}, nil
}

type oidcDeleteOut struct {
	Status int
}

func (s *Service) adminDeleteOIDC(ctx context.Context, in *oidcIDIn) (*oidcDeleteOut, error) {
	if err := tenancy.Require(ctx, tenancy.PermInstanceAdmin); err != nil {
		return nil, err
	}
	before, err := s.getOIDCProvider(ctx, in.ProviderID)
	if err != nil {
		return nil, err
	}
	if err := s.deleteOIDCProvider(ctx, in.ProviderID); err != nil {
		return nil, err
	}
	s.auditProviderAdmin(ctx, "oidc.provider.deleted", in.ProviderID, audit.Diff(before, nil))
	return &oidcDeleteOut{Status: http.StatusNoContent}, nil
}
