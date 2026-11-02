package store

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// ErrNoTenant is returned when a tenant-owned query runs without an organisation in the context. It is a
// programming error (a handler forgot the tenant middleware); the HTTP layer maps it to internal_error.
var ErrNoTenant = errors.New("store: no organisation in context")

type orgKey struct{}

// WithOrganization stores the caller's organisation ID in ctx. Only the auth/tenancy middleware calls it,
// from the authenticated session or API key — never from request bodies (S1 §1.2).
func WithOrganization(ctx context.Context, orgID string) context.Context {
	return context.WithValue(ctx, orgKey{}, orgID)
}

// OrganizationFrom returns the organisation ID in ctx.
func OrganizationFrom(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(orgKey{}).(string)
	return id, ok && id != ""
}

// Scoped returns filter with organizationId set from ctx. Every repo query on a tenant-owned collection
// goes through it (the tenant guard, S1 §1.2). A filter that already names a different organizationId is
// rejected rather than silently overridden.
func Scoped(ctx context.Context, filter bson.M) (bson.M, error) {
	org, ok := OrganizationFrom(ctx)
	if !ok {
		return nil, ErrNoTenant
	}
	out := bson.M{}
	for k, v := range filter {
		out[k] = v
	}
	if existing, has := out["organizationId"]; has && existing != org {
		return nil, errors.New("store: filter names a different organisation than the context")
	}
	out["organizationId"] = org
	return out, nil
}
