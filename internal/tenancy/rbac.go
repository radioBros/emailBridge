// Package tenancy owns organisations, users' EmailBridge profiles, memberships and authorisation: who
// the caller is, which organisation a request acts in, and whether a role or API-key scope permits an
// action (S10 §4, S1 §2).
package tenancy

import (
	"context"
	"slices"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

// Role is an organisation role (S10 §4).
type Role string

// Roles, most powerful first.
const (
	RoleOwner    Role = "owner"
	RoleAdmin    Role = "admin"
	RoleOperator Role = "operator"
	RoleViewer   Role = "viewer"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleOperator, RoleViewer:
		return true
	}
	return false
}

// Permission is one capability row of the S10 §4 matrix.
type Permission string

// Permissions.
const (
	PermRead             Permission = "read"               // read everything in the organisation (except secrets)
	PermAccountsWrite    Permission = "accounts.write"     // create, edit, delete accounts
	PermAccountsOperate  Permission = "accounts.operate"   // sync, reconnect, resume
	PermMessagesWrite    Permission = "messages.write"     // mailbox/message mutations, drafts, upload (P4)
	PermSend             Permission = "outbound.write"     // send / cancel outbound, sandbox actions
	PermWebhooksWrite    Permission = "webhooks.write"     // webhooks, routes, deliveries replay
	PermProtocolLogs     Permission = "protocolLogs.write" // protocol logs on / raw debug
	PermSettingsWrite    Permission = "settings.write"     // organisation settings and switches
	PermAPIKeysWrite     Permission = "apikeys.write"      // API keys
	PermMembersWrite     Permission = "members.write"      // invite, change roles (admins: not owners)
	PermAuditRead        Permission = "audit.read"         // audit log
	PermLogsRead         Permission = "logs.read"          // logs
	PermOrgDelete        Permission = "organization.delete"
	PermInstanceAdmin    Permission = "instance.admin"     // global settings, organisations, locks
	PermClusterRead      Permission = "cluster.read"       // GET /v1/cluster (instance owner)
	PermClusterOperate   Permission = "cluster.operate"    // drain, queues pause/resume (instance owner)
	PermOauthTokenExport Permission = "oauth.token.export" //nolint:gosec // G101: permission name, not a credential
)

var matrix = map[Role][]Permission{
	RoleOwner: {PermRead, PermAccountsWrite, PermAccountsOperate, PermMessagesWrite, PermSend, PermWebhooksWrite, PermProtocolLogs,
		PermSettingsWrite, PermAPIKeysWrite, PermMembersWrite, PermAuditRead, PermLogsRead, PermOrgDelete, PermOauthTokenExport},
	RoleAdmin: {PermRead, PermAccountsWrite, PermAccountsOperate, PermMessagesWrite, PermSend, PermWebhooksWrite, PermProtocolLogs,
		PermSettingsWrite, PermAPIKeysWrite, PermMembersWrite, PermAuditRead, PermLogsRead, PermOauthTokenExport},
	RoleOperator: {PermRead, PermAccountsWrite, PermAccountsOperate, PermMessagesWrite, PermSend, PermWebhooksWrite, PermAuditRead, PermLogsRead},
	RoleViewer:   {PermRead},
}

// RoleAllows reports whether role r has permission p.
func RoleAllows(r Role, p Permission) bool { return slices.Contains(matrix[r], p) }

// Permissions returns the permissions of a role (for GET /v1/me).
func Permissions(r Role) []Permission { return append([]Permission(nil), matrix[r]...) }

// scopeFor maps a permission to the API-key scope that grants it (S10 §3); "" = never via API keys.
var scopeFor = map[Permission]string{ //nolint:gosec // G101: scope name strings, not credentials
	PermAccountsWrite:    "accounts:write",
	PermAccountsOperate:  "accounts:write",
	PermMessagesWrite:    "messages:write",
	PermSend:             "outbound:write",
	PermWebhooksWrite:    "webhooks:write",
	PermSettingsWrite:    "settings:write",
	PermAPIKeysWrite:     "apikeys:write",
	PermMembersWrite:     "members:write",
	PermAuditRead:        "audit:read",
	PermLogsRead:         "logs:read",
	PermProtocolLogs:     "accounts:write",
	PermOauthTokenExport: "oauth:token:export",
}

// CallerKind distinguishes how a request authenticated.
type CallerKind string

// Caller kinds.
const (
	CallerUser   CallerKind = "user"
	CallerAPIKey CallerKind = "apiKey"
	CallerSystem CallerKind = "system"
)

// Caller is the authenticated principal of a request, resolved once by the middleware.
type Caller struct {
	Kind           CallerKind
	UserID         string // usr_… (users) — empty for API keys
	AuthUserID     string // Limen user ID
	Email          string
	APIKeyID       string // key_… (API keys)
	OrganizationID string
	Role           Role // users: membership role in OrganizationID
	InstanceOwner  bool
	Scopes         []string // API keys
	AccountID      string   // API key bound to one account (S10 §3)
	MFA            bool
}

// Actor is the ID logged and audited for the caller.
func (c Caller) Actor() string {
	switch c.Kind {
	case CallerAPIKey:
		return c.APIKeyID
	case CallerSystem:
		return "system"
	}
	return c.UserID
}

type callerKey struct{}

// WithCaller stores c in ctx together with the tenant guard organisation (S1 §1.2) and the log actor.
func WithCaller(ctx context.Context, c Caller) context.Context {
	ctx = context.WithValue(ctx, callerKey{}, c)
	if c.OrganizationID != "" {
		ctx = store.WithOrganization(ctx, c.OrganizationID)
	}
	return logging.With(ctx, func(id *logging.IDs) {
		id.Actor = c.Actor()
		id.OrganizationID = c.OrganizationID
	})
}

// CallerFrom returns the caller of ctx.
func CallerFrom(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(callerKey{}).(Caller)
	return c, ok
}

// hasScope reports whether scopes grant an org-wide want (`resource:mode`). Resource-specific
// scopes alone do not satisfy an org-wide want. Multi-segment wants (e.g. oauth:token:export) match
// only as an exact string (or admin).
func hasScope(scopes []string, want string) bool {
	if want == "" {
		return false
	}
	for _, s := range scopes {
		if s == "admin" || s == want {
			return true
		}
	}
	res, mode, ok := strings.Cut(want, ":")
	if !ok || strings.Contains(mode, ":") {
		return false
	}
	for _, s := range scopes {
		if mode == "read" && s == res+":write" { // write implies read
			return true
		}
	}
	return false
}

// hasScopeOn reports whether scopes grant want on resourceID. Org-wide scopes cover every id;
// resource-specific scopes must match resourceID. want is `resource:mode` (no id).
func hasScopeOn(scopes []string, want, resourceID string) bool {
	if hasScope(scopes, want) {
		return true
	}
	if want == "" || resourceID == "" {
		return false
	}
	res, mode, ok := strings.Cut(want, ":")
	if !ok || strings.Contains(mode, ":") {
		return false
	}
	specific := want + ":" + resourceID
	writeSpecific := res + ":write:" + resourceID
	for _, s := range scopes {
		if s == "admin" || s == specific {
			return true
		}
		if mode == "read" && s == writeSpecific {
			return true
		}
	}
	return false
}

// scopeKindForID maps a resource id prefix to the scope resource name used in granular scopes.
func scopeKindForID(resourceID string) string {
	p, _, err := store.ParseID(resourceID)
	if err != nil {
		return ""
	}
	switch p {
	case store.PrefixAccount:
		return "accounts"
	case store.PrefixEndpoint:
		return "webhooks"
	case store.PrefixRoute:
		return "routes"
	case store.PrefixTemplate:
		return "templates"
	case store.PrefixSuppression:
		return "suppression-lists"
	default:
		return ""
	}
}

// Allows reports whether the caller may perform p at organisation scope.
func (c Caller) Allows(p Permission) bool {
	switch c.Kind {
	case CallerSystem:
		return true
	case CallerAPIKey:
		if p == PermRead {
			// Reads name their resource scope (RequireRead); a bare PermRead check is only satisfied by admin
			// keys, so a handler that forgot its scope fails closed instead of opening the organisation.
			return slices.Contains(c.Scopes, "admin")
		}
		return hasScope(c.Scopes, scopeFor[p])
	case CallerUser:
		if p == PermInstanceAdmin || p == PermClusterRead || p == PermClusterOperate {
			return c.InstanceOwner
		}
		return RoleAllows(c.Role, p)
	}
	return false
}

// AllowsOn reports whether the caller may perform p on resourceID (P3.4a granular scopes).
func (c Caller) AllowsOn(p Permission, resourceID string) bool {
	switch c.Kind {
	case CallerSystem:
		return true
	case CallerUser:
		return c.Allows(p)
	case CallerAPIKey:
		if p == PermRead {
			// As in Allows: reads use RequireReadOn with their scope; a bare PermRead is admin-only for keys.
			if kind := scopeKindForID(resourceID); kind != "" {
				return hasScopeOn(c.Scopes, kind+":read", resourceID)
			}
			return slices.Contains(c.Scopes, "admin")
		}
		want := scopeFor[p]
		if want == "" {
			return false
		}
		return hasScopeOn(c.Scopes, want, resourceID)
	}
	return false
}

// AllowsRead reports whether the caller may read the S10 §3 resource scope (`events`, `messages`, …) across
// the organisation. Users need PermRead (every role has it); API keys need `<scope>:read`, `<scope>:write` or
// `admin` — holding some other scope is not enough.
func (c Caller) AllowsRead(scope string) bool {
	switch c.Kind {
	case CallerSystem:
		return true
	case CallerUser:
		return c.Allows(PermRead)
	case CallerAPIKey:
		return hasScope(c.Scopes, scope+":read")
	}
	return false
}

// AllowsReadOn is AllowsRead for one resource: API keys may also hold the granular `<scope>:read:<id>` (or
// `:write:<id>`) scope for it.
func (c Caller) AllowsReadOn(scope, resourceID string) bool {
	if c.Kind != CallerAPIKey {
		return c.AllowsRead(scope)
	}
	return hasScopeOn(c.Scopes, scope+":read", resourceID)
}

func readDenied(c Caller, scope string) error {
	if c.Kind == CallerAPIKey {
		return errs.New(errs.CodeScopeMissing, "The API key lacks the scope this operation needs.").WithSafe("scope", scope+":read")
	}
	return errs.New(errs.CodeForbidden, "Your role does not allow this operation.").WithSafe("permission", string(PermRead))
}

// RequireRead returns nil when the caller may read scope across the organisation (S10 §3–4). Every read
// handler names the resource scope it reads, so an API key only sees what its scopes grant.
func RequireRead(ctx context.Context, scope string) error {
	c, ok := CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.AllowsRead(scope) {
		return nil
	}
	return readDenied(c, scope)
}

// RequireReadOn is RequireRead for one resource (granular API-key scopes).
func RequireReadOn(ctx context.Context, scope, resourceID string) error {
	c, ok := CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.AllowsReadOn(scope, resourceID) {
		return nil
	}
	return readDenied(c, scope)
}

// RequireReadAccount is RequireReadOn for data that belongs to accountID (the account itself, its messages,
// logs, exports …) and additionally refuses account-bound API keys acting on another account. A granular
// `accounts:read:<id>` scope grants the account's own record, not its messages: those need `messages:read`.
func RequireReadAccount(ctx context.Context, scope, accountID string) error {
	if err := RequireReadOn(ctx, scope, accountID); err != nil {
		return err
	}
	c, _ := CallerFrom(ctx)
	if c.Kind == CallerAPIKey && c.AccountID != "" && c.AccountID != accountID {
		return errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
	}
	return nil
}

// Require returns nil when the caller in ctx may perform p, otherwise an unauthenticated or forbidden
// error (S2 codes). Every handler and WebSocket topic calls it (S10 §4).
func Require(ctx context.Context, p Permission) error {
	c, ok := CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.Allows(p) {
		return nil
	}
	if c.Kind == CallerAPIKey {
		return errs.New(errs.CodeScopeMissing, "The API key lacks the scope this operation needs.").WithSafe("permission", string(p))
	}
	return errs.New(errs.CodeForbidden, "Your role does not allow this operation.").WithSafe("permission", string(p))
}

// RequireOn is Require with per-resource API-key scopes (P3.4a).
func RequireOn(ctx context.Context, p Permission, resourceID string) error {
	c, ok := CallerFrom(ctx)
	if !ok {
		return errs.New(errs.CodeUnauthenticated, "Authentication is required.")
	}
	if c.AllowsOn(p, resourceID) {
		return nil
	}
	if c.Kind == CallerAPIKey {
		return errs.New(errs.CodeScopeMissing, "The API key lacks the scope this operation needs.").
			WithSafe("permission", string(p)).WithSafe("resourceId", resourceID)
	}
	return errs.New(errs.CodeForbidden, "Your role does not allow this operation.").WithSafe("permission", string(p))
}

// RequireAccount additionally refuses account-bound API keys acting on another account.
func RequireAccount(ctx context.Context, p Permission, accountID string) error {
	if err := RequireOn(ctx, p, accountID); err != nil {
		return err
	}
	c, _ := CallerFrom(ctx)
	if c.Kind == CallerAPIKey && c.AccountID != "" && c.AccountID != accountID {
		return errs.New(errs.CodeForbidden, "The API key is bound to a different account.")
	}
	return nil
}

// ScopedIDs returns the resource ids an API-key caller may access for kind
// ("accounts", "webhooks", "routes", "templates", "deliveries"). limited=false means unrestricted;
// limited=true with an empty slice means the key sees nothing of that kind.
func ScopedIDs(ctx context.Context, kind string) (ids []string, limited bool) {
	c, ok := CallerFrom(ctx)
	if !ok || c.Kind != CallerAPIKey {
		return nil, false
	}
	return scopeResourceIDs(c.Scopes, kind)
}

func scopeResourceIDs(scopes []string, kind string) (ids []string, limited bool) {
	prefix := kind + ":"
	for _, s := range scopes {
		if s == "admin" || s == kind+":read" || s == kind+":write" {
			return nil, false
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range scopes {
		if !strings.HasPrefix(s, prefix) {
			continue
		}
		parts := strings.SplitN(s, ":", 3)
		if len(parts) != 3 || parts[2] == "" {
			continue
		}
		if parts[1] != "read" && parts[1] != "write" {
			continue
		}
		if !seen[parts[2]] {
			seen[parts[2]] = true
			out = append(out, parts[2])
		}
	}
	return out, true
}
