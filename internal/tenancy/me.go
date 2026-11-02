package tenancy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/i18n"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/sysmail"
)

// MeMembership is one membership in GET /v1/me.
type MeMembership struct {
	OrganizationID   string `json:"organizationId" doc:"Organisation ID." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	OrganizationName string `json:"organizationName" doc:"Organisation display name." example:"Acme Legal"`
	Role             Role   `json:"role" doc:"The user's role there: owner, admin, operator or viewer." example:"admin"`
}

// MeOutput is the GET /v1/me response.
type MeOutput struct {
	Body struct {
		Kind                 CallerKind     `json:"kind" enum:"user,apiKey,system" doc:"Who is calling: a signed-in user or an API key." example:"user"`
		User                 *User          `json:"user,omitempty" doc:"The signed-in user's profile (users only)."`
		APIKeyID             string         `json:"apiKeyId,omitempty" doc:"ID of the calling API key (API keys only)." example:"key_01m5cqe6d0rs9cyxvy69ynf0t6"`
		Scopes               []string       `json:"scopes,omitempty" doc:"Scopes of the calling API key (API keys only)." example:"[\"accounts:read\",\"outbound:write\"]"`
		ActiveOrganizationID string         `json:"activeOrganizationId,omitempty" doc:"Organisation this request acts in (EB-Organization or the last used one)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
		Role                 Role           `json:"role,omitempty" doc:"Role in the active organisation (users only)." example:"admin"`
		Permissions          []Permission   `json:"permissions" doc:"Effective permissions in the active organisation (role or key scopes; instance owners also get instance.admin)." example:"[\"read\",\"accounts.write\",\"accounts.operate\",\"settings.write\",\"members.write\"]"`
		Memberships          []MeMembership `json:"memberships" doc:"Every organisation the user belongs to (empty for API keys)."`
		MFA                  bool           `json:"mfa" doc:"Whether the session was completed with a second factor." example:"true"`
	}
}

// JobCreator queues a long-running job (wired to jobs.Service.Create in cmd). Returns the new job ID.
type JobCreator func(ctx context.Context, kind string, payload any) (jobID string, err error)

// Service exposes tenancy operations to other modules.
type Service struct {
	Repo      *Repo
	Audit     Auditor
	Mail      *sysmail.Service
	Log       *slog.Logger
	PublicURL string
	Jobs      JobCreator
}

// Me builds the GET /v1/me response for the caller in ctx.
func (s *Service) Me(ctx context.Context) (*MeOutput, error) {
	c, ok := CallerFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	out := &MeOutput{}
	b := &out.Body
	b.Kind, b.ActiveOrganizationID, b.Role, b.MFA, b.Memberships = c.Kind, c.OrganizationID, c.Role, c.MFA, []MeMembership{}
	switch c.Kind {
	case CallerAPIKey:
		b.APIKeyID, b.Scopes = c.APIKeyID, c.Scopes
		b.Permissions = []Permission{}
		for _, p := range allPermissions {
			if c.Allows(p) {
				b.Permissions = append(b.Permissions, p)
			}
		}
	default:
		u, err := s.Repo.UserByAuthID(ctx, c.AuthUserID)
		if err != nil {
			return nil, err
		}
		b.User = u
		b.Permissions = Permissions(c.Role)
		if c.InstanceOwner {
			b.Permissions = append(b.Permissions, PermInstanceAdmin)
		}
		ms, err := s.Repo.MembershipsOf(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(ms))
		for _, m := range ms {
			ids = append(ids, m.OrganizationID)
		}
		orgs, err := s.Repo.Organizations(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			b.Memberships = append(b.Memberships, MeMembership{OrganizationID: m.OrganizationID, OrganizationName: orgs[m.OrganizationID].Name, Role: m.Role})
		}
	}
	return out, nil
}

// MePatch is the body of PATCH /v1/me.
type MePatch struct {
	Name   *string `json:"name,omitempty" doc:"New display name, 1–100 characters." example:"Maria Rossi"`
	Locale *string `json:"locale,omitempty" doc:"UI and notification language; one of the supported locales (en, it, es, fr, de, pt, ru, zh, ja)." example:"it"`
}

type mePatchIn struct {
	Body MePatch
}

// PatchMe updates the caller's profile (name, locale) and returns the GET /v1/me shape.
func (s *Service) PatchMe(ctx context.Context, in MePatch) (*MeOutput, error) {
	c, ok := CallerFrom(ctx)
	if !ok {
		return nil, errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Kind != CallerUser || c.UserID == "" {
		return nil, errs.New(errs.CodeForbidden, "Profile updates require a signed-in user.")
	}
	var name, locale *string
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if r := utf8.RuneCountInString(n); r < 1 || r > 100 {
			return nil, errs.New(errs.CodeValidationFailed, "Name must be between 1 and 100 characters.").
				WithSafe("field", "name")
		}
		name = &n
	}
	if in.Locale != nil {
		l := strings.TrimSpace(*in.Locale)
		if !i18n.IsSupported(l) {
			return nil, errs.New(errs.CodeValidationFailed, "Locale must be one of the supported UI languages.").
				WithSafe("field", "locale")
		}
		l = i18n.Normalize(l)
		locale = &l
	}
	u, err := s.Repo.UserByID(ctx, c.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errs.New(errs.CodeNotFound, "No such user.")
		}
		return nil, err
	}
	set := bson.M{}
	changes := map[string]FieldDiff{}
	if name != nil && *name != u.Name {
		set["name"] = *name
		changes["name"] = FieldDiff{Before: u.Name, After: *name}
	}
	if locale != nil && *locale != u.Locale {
		set["locale"] = *locale
		changes["locale"] = FieldDiff{Before: u.Locale, After: *locale}
	}
	if len(set) > 0 {
		if _, err := s.Repo.UpdateUser(ctx, u.ID, u.Version, set); err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, errs.New(errs.CodeConflict, "The profile was updated concurrently; retry.")
			}
			return nil, err
		}
		s.audit(ctx, "user.update", "user", u.ID, changes)
	}
	return s.Me(ctx)
}

var allPermissions = []Permission{PermRead, PermAccountsWrite, PermAccountsOperate, PermSend, PermWebhooksWrite, PermProtocolLogs,
	PermSettingsWrite, PermAPIKeysWrite, PermMembersWrite, PermAuditRead, PermLogsRead, PermOrgDelete}

// UserCreator creates the Limen user behind a profile (internal/auth.Service.CreateUser).
type UserCreator func(ctx context.Context, email, password string) (authUserID string, err error)

// Bootstrap creates the first organisation, its owner (instance owner too) and the owner's sign-in when
// the instance has no users yet (S10 §2.4). It is a no-op otherwise.
func (s *Service) Bootstrap(ctx context.Context, create UserCreator, email, password, orgName string) (bool, error) {
	if email == "" || password == "" {
		return false, nil
	}
	n, err := s.Repo.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	authID, err := create(ctx, email, password)
	if err != nil {
		return false, fmt.Errorf("tenancy: bootstrap owner sign-in: %w", err)
	}
	org, err := s.Repo.CreateOrganization(ctx, orgName, Slugify(orgName))
	if err != nil {
		return false, err
	}
	u := &User{AuthUserID: authID, Email: email, Name: email, InstanceRole: "owner", LastOrganizationID: org.ID}
	if err := s.Repo.CreateUser(ctx, u); err != nil {
		return false, err
	}
	if _, err := s.Repo.CreateMembership(ctx, org.ID, u.ID, RoleOwner); err != nil {
		return false, err
	}
	return true, nil
}

// Module wires the resolver middleware, /v1/me, organisations/members APIs and invitation accept
// (role api), and runs the owner bootstrap.
type Module struct {
	Identity   IdentitySource
	Keys       KeyResolver
	Creator    UserCreator
	PublicURL  string      // EB_PUBLIC_URL — invitation links use /accept-invite#<token>
	InviteRate config.Rate // EB_RATE_LOGIN; Count/Period default to 10/min when zero
	Jobs       JobCreator  // jobs.Service.Create wrapper; required for organisation delete
	Bootstrap  struct{ Email, Password, OrgName string }
	service    *Service
	inviteLim  *inviteLimiter
	exportLim  *inviteLimiter
	exportOnce sync.Once
}

// Name implements app.Module.
func (m *Module) Name() string { return "tenancy" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	aud, _ := app.Get[Auditor](a)
	mail, _ := app.Get[*sysmail.Service](a)
	log := a.Log
	repo := &Repo{DB: db}
	m.service = &Service{Repo: repo, Audit: aud, Mail: mail, Log: log, PublicURL: m.PublicURL, Jobs: m.Jobs}
	if mail != nil {
		mail.LocaleByEmail = func(ctx context.Context, email string) string {
			return localeForEmail(ctx, db, repo, email)
		}
	}
	app.Provide(a, m.service)
	rv := &Resolver{Repo: m.service.Repo, Identity: m.Identity, Keys: m.Keys, Write: httpapi.WriteProblem}
	srv.AddMiddleware(rv.Middleware)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getMe", Method: http.MethodGet, Path: "/v1/me", Summary: "Current caller",
		Description: "Describes who is calling: for a signed-in user the profile, the active organisation and role, the " +
			"effective permissions and every membership; for an API key its ID, scopes and the permissions they " +
			"grant. Any authenticated caller; no permission needed. The admin UI uses it to decide which screens " +
			"to show. Read-only.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeSessionExpired, errs.CodeAPIKeyRevoked, errs.CodeNotFound),
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {
			Examples: map[string]*huma.Example{"user": {Summary: "Signed-in user", Value: meUserExample}, "apiKey": {Summary: "API key", Value: meKeyExample}},
		}}}},
		Tags: []string{"Me"}, Extensions: httpapi.Phase("P0"),
	}, func(ctx context.Context, _ *struct{}) (*MeOutput, error) { return m.service.Me(ctx) })
	httpapi.Register(srv, huma.Operation{
		OperationID: "patchMe", Method: http.MethodPatch, Path: "/v1/me", Summary: "Update profile",
		Description: "Changes the signed-in user's display name and/or UI language; omitted fields stay unchanged. Users " +
			"only (API keys get `403 forbidden`). The new locale also applies to system emails sent to the user. " +
			"Writes a `user.update` audit record when something changed and returns the `GET /v1/me` shape.",
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeValidationFailed, errs.CodeConflict, errs.CodeNotFound),
		Tags:     []string{"Me"}, Extensions: httpapi.Phase("P0"),
	}, func(ctx context.Context, in *mePatchIn) (*MeOutput, error) { return m.service.PatchMe(ctx, in.Body) })
	m.registerOrgs(srv)
	m.registerMembers(srv)
	m.registerInvite(srv)
	if m.Creator != nil {
		created, err := m.service.Bootstrap(ctx, m.Creator, m.Bootstrap.Email, m.Bootstrap.Password, m.Bootstrap.OrgName)
		if err != nil && !errors.Is(err, context.Canceled) {
			return err
		}
		if created {
			a.Log.Warn("bootstrap owner created; remove EB_BOOTSTRAP_OWNER_PASSWORD from the environment", slog.String("organization", m.Bootstrap.OrgName))
		}
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Whole-body examples for GET /v1/me (documentation only).
var (
	meUserExample = map[string]any{
		"kind": "user",
		"user": map[string]any{
			"id": "usr_01m5ccvej0dqefkn0xhkcq2q6z", "email": "maria.rossi@example.com", "name": "Maria Rossi",
			"locale": "it", "instanceRole": "none", "status": "active", "lastLoginAt": "2026-10-26T08:15:42Z",
			"createdAt": "2026-10-20T09:00:00Z", "updatedAt": "2026-10-24T14:32:10Z", "version": 2,
		},
		"activeOrganizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"role":                 "admin",
		"permissions":          []any{"read", "accounts.write", "accounts.operate", "messages.write", "outbound.write", "webhooks.write", "settings.write", "apikeys.write", "members.write", "audit.read", "logs.read"},
		"memberships": []any{
			map[string]any{"organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "organizationName": "Acme Legal", "role": "admin"},
			map[string]any{"organizationId": "org_01m5cxsea0fk66qqqft56j4zm7", "organizationName": "Studio Bianchi", "role": "viewer"},
		},
		"mfa": true,
	}
	meKeyExample = map[string]any{ //nolint:gosec // G101: documentation example, not a credential
		"kind":                 "apiKey",
		"apiKeyId":             "key_01m5cqe6d0rs9cyxvy69ynf0t6",
		"scopes":               []any{"accounts:read", "outbound:write"},
		"activeOrganizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"permissions":          []any{"read", "outbound.write"},
		"memberships":          []any{},
		"mfa":                  false,
	}
)
