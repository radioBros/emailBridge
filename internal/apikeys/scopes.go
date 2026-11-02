package apikeys

import (
	"context"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// knownResources are the S10 §3 scope resources (P0 set; later phases add more).
var knownResources = map[string]bool{
	"organizations": true, "members": true, "apikeys": true, "audit": true,
	"settings": true, "logs": true, "jobs": true, "accounts": true,
	"messages": true, "outbound": true, "sandbox": true, "webhooks": true,
	"routes": true, "deliveries": true, "events": true, "templates": true,
	"gateways": true, "suppression": true, "oauth": true, "exports": true,
	"integrations": true, "me": true, "mcp": true,
}

// granularKinds map scope resource → ID prefix + Mongo collection for existence checks (P3.4a).
var granularKinds = map[string]struct {
	Prefix     store.Prefix
	Collection string
}{
	"accounts":  {store.PrefixAccount, "accounts"},
	"webhooks":  {store.PrefixEndpoint, "webhookEndpoints"},
	"routes":    {store.PrefixRoute, "routes"},
	"templates": {store.PrefixTemplate, "templates"},
}

// parsedScope is one validated scope token.
type parsedScope struct {
	Raw      string
	Resource string // empty when Admin
	Mode     string // read|write
	ID       string // optional resource id
	Admin    bool
}

func parseScope(s string) (parsedScope, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return parsedScope{}, false
	}
	if s == "admin" {
		return parsedScope{Raw: s, Admin: true}, true
	}
	// W54: non-granular three-segment scope for token export.
	if s == "oauth:token:export" {
		return parsedScope{Raw: s, Resource: "oauth", Mode: "token:export"}, true
	}
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		res, mode := parts[0], parts[1]
		if !knownResources[res] || !validScopeMode(res, mode) {
			return parsedScope{}, false
		}
		return parsedScope{Raw: s, Resource: res, Mode: mode}, true
	case 3:
		res, mode, id := parts[0], parts[1], parts[2]
		meta, ok := granularKinds[res]
		if !ok || (mode != "read" && mode != "write") || id == "" || !store.IsID(meta.Prefix, id) {
			return parsedScope{}, false
		}
		return parsedScope{Raw: s, Resource: res, Mode: mode, ID: id}, true
	default:
		return parsedScope{}, false
	}
}

// validScopeMode accepts read|write for normal resources; mcp also allows use (P7.3).
func validScopeMode(res, mode string) bool {
	if mode == "read" || mode == "write" {
		return true
	}
	return res == "mcp" && mode == "use"
}

func validateScopes(scopes []string) error {
	if len(scopes) == 0 {
		return errs.New(errs.CodeValidationFailed, "At least one scope is required.")
	}
	seen := map[string]bool{}
	for _, s := range scopes {
		p, ok := parseScope(s)
		if !ok {
			return errs.New(errs.CodeValidationFailed, "Unknown or malformed scope.").WithSafe("scope", s)
		}
		if seen[p.Raw] {
			return errs.New(errs.CodeValidationFailed, "Scopes must be non-empty and unique.").WithSafe("scope", p.Raw)
		}
		seen[p.Raw] = true
	}
	return nil
}

// validateScopeResources ensures every granular resource id exists in the caller's organisation.
func (s *Service) validateScopeResources(ctx context.Context, scopes []string) error {
	for _, raw := range scopes {
		p, ok := parseScope(raw)
		if !ok || p.ID == "" {
			continue
		}
		meta := granularKinds[p.Resource]
		filter, err := store.Scoped(ctx, bson.M{"_id": p.ID})
		if err != nil {
			return err
		}
		err = s.DB.Collection(meta.Collection).FindOne(ctx, filter).Err()
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errs.New(errs.CodeValidationFailed, "Scoped resource does not exist in this organisation.").
				WithSafe("scope", p.Raw)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// holdsScope reports whether held grants want (write implies read; admin grants all;
// org-wide grants every resource id of that kind).
func holdsScope(held []string, want string) bool {
	wp, ok := parseScope(want)
	if !ok {
		return false
	}
	if wp.Admin {
		for _, s := range held {
			if s == "admin" {
				return true
			}
		}
		return false
	}
	for _, s := range held {
		hp, ok := parseScope(s)
		if !ok {
			continue
		}
		if hp.Admin {
			return true
		}
		if hp.Resource != wp.Resource {
			continue
		}
		// org-wide held covers any want of same resource
		if hp.ID == "" {
			if hp.Mode == "write" || hp.Mode == wp.Mode {
				return true
			}
			continue
		}
		// resource-specific held only covers the same id
		if wp.ID != "" && hp.ID == wp.ID && (hp.Mode == "write" || hp.Mode == wp.Mode) {
			return true
		}
	}
	return false
}

func scopesSubset(held, requested []string) error {
	for _, want := range requested {
		if !holdsScope(held, want) {
			return errs.New(errs.CodeScopeMissing, "The API key lacks a scope needed to grant it.").WithSafe("scope", want)
		}
	}
	return nil
}
