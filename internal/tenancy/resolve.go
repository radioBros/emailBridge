package tenancy

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Identity is what the session layer (internal/auth) knows about a signed-in user.
type Identity struct {
	AuthUserID string
	Email      string
	MFA        bool
}

// IdentitySource reads the session identity from a request context (wired to auth.IdentityFrom).
type IdentitySource func(ctx context.Context) (Identity, bool)

// KeyResolver turns a bearer token into an API-key caller (P0.13). It returns (nil, nil) when the
// request has no bearer token.
type KeyResolver func(r *http.Request) (*Caller, error)

// ErrorWriter writes a problem response (httpapi.WriteProblem).
type ErrorWriter func(w http.ResponseWriter, r *http.Request, err error)

// Resolver builds the Caller of every request: API key first (Authorization: Bearer), otherwise the
// session identity mapped to the EmailBridge profile, its memberships and the active organisation
// (EB-Organization header, else the last used, else the first membership). Unauthenticated requests pass
// through without a caller; handlers call Require.
type Resolver struct {
	Repo     *Repo
	Identity IdentitySource
	Keys     KeyResolver
	Write    ErrorWriter
}

// Middleware implements the resolution.
func (rv *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := rv.resolve(r)
		if err != nil {
			rv.Write(w, r, err)
			return
		}
		if c != nil {
			r = r.WithContext(WithCaller(r.Context(), *c))
		} else if requiresCaller(r.URL.Path) {
			// Authentication is checked before anything else reads the request: an anonymous call never reaches
			// body parsing, validation or a lookup (it used to get 422s that described the request schemas).
			rv.Write(w, r, errs.New(errs.CodeUnauthenticated, "Authentication is required."))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// publicAPIPrefixes are the /v1 routes that answer without a caller by design: sign-in (Limen, SSO), the
// hosted connect page and its OAuth redeem, and invitation acceptance. Everything else under /v1 needs a
// session or an API key (S10 §4). Signed links (/e/, /a/), tracking and callbacks live outside /v1.
var publicAPIPrefixes = []string{
	"/v1/auth/", "/v1/public/", "/v1/invitations/accept", "/v1/invitations/lookup",
	"/v1/openapi", // the published API description (openapi.json / .yaml), S2
}

func requiresCaller(path string) bool {
	if !strings.HasPrefix(path, "/v1/") {
		return false
	}
	for _, p := range publicAPIPrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	// GET /v1/connect-links/{token}/redeem: the hosted connect page's OAuth hand-off (P6.4).
	if strings.HasPrefix(path, "/v1/connect-links/") && strings.HasSuffix(path, "/redeem") {
		return false
	}
	return true
}

func (rv *Resolver) resolve(r *http.Request) (*Caller, error) {
	ctx := r.Context()
	if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		if rv.Keys == nil {
			return nil, errs.New(errs.CodeAPIKeyInvalid, "API keys are not available yet.")
		}
		c, err := rv.Keys(r)
		if err != nil {
			return nil, err
		}
		if c != nil {
			if org := r.Header.Get("EB-Organization"); org != "" && org != c.OrganizationID {
				return nil, errs.New(errs.CodeOrganizationMismatch, "The API key belongs to a different organisation.")
			}
			return c, nil
		}
	}
	if rv.Identity == nil {
		return nil, nil
	}
	id, ok := rv.Identity(ctx)
	if !ok {
		return nil, nil
	}
	u, err := rv.Repo.UserByAuthID(ctx, id.AuthUserID)
	if errors.Is(err, ErrNotFound) {
		return nil, errs.New(errs.CodeUnauthenticated, "No EmailBridge profile is linked to this sign-in.")
	}
	if err != nil {
		return nil, err
	}
	if u.Status != "active" {
		return nil, errs.New(errs.CodeForbidden, "This user is disabled.")
	}
	c := &Caller{Kind: CallerUser, UserID: u.ID, AuthUserID: id.AuthUserID, Email: u.Email, InstanceOwner: u.InstanceRole == "owner", MFA: id.MFA}
	ms, err := rv.Repo.MembershipsOf(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	want := r.Header.Get("EB-Organization")
	pick := func(orgID string) bool {
		for _, m := range ms {
			if m.OrganizationID == orgID {
				c.OrganizationID, c.Role = m.OrganizationID, m.Role
				return true
			}
		}
		return false
	}
	switch {
	case want != "":
		if !pick(want) && !c.InstanceOwner {
			return nil, errs.New(errs.CodeOrganizationMismatch, "You are not a member of that organisation.")
		}
		if c.OrganizationID == "" { // instance owner acting in another organisation: read + admin only
			c.OrganizationID, c.Role = want, RoleViewer
		}
	case u.LastOrganizationID != "" && pick(u.LastOrganizationID):
	case len(ms) > 0:
		pick(ms[0].OrganizationID)
	}
	if want != "" && c.OrganizationID == want && want != u.LastOrganizationID {
		_ = rv.Repo.SetLastOrganization(ctx, u.ID, want)
	}
	return c, nil
}
