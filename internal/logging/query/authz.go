package query

import (
	"context"

	"github.com/radiobros/emailbridge/internal/errs"
	tenant "github.com/radiobros/emailbridge/internal/store"
)

// Authorizer identifies the caller of a log API request (S12 §8 tenant scoping). orgID is the caller's
// active organisation; instanceOwner callers see every record, including instance-level ones without an
// organisation. Errors should be errs errors (they become the problem response).
type Authorizer interface {
	Caller(ctx context.Context) (orgID string, instanceOwner bool, err error)
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(ctx context.Context) (string, bool, error)

// Caller implements Authorizer.
func (f AuthorizerFunc) Caller(ctx context.Context) (string, bool, error) { return f(ctx) }

// TenantAuthorizer is the default until RBAC lands (P0.14): the organisation from the tenancy context,
// never an instance owner. Without an organisation the caller is refused.
type TenantAuthorizer struct{}

// Caller implements Authorizer.
func (TenantAuthorizer) Caller(ctx context.Context) (string, bool, error) {
	org, ok := tenant.OrganizationFrom(ctx)
	if !ok {
		return "", false, errs.New(errs.CodeForbidden, "No organisation is selected for this request.")
	}
	return org, false, nil
}

// scope resolves the organisation a request may see. Instance owners get the organisation they asked
// for ("" = all records); everyone else gets their own, and asking for another is refused.
func scope(ctx context.Context, a Authorizer, requested string) (org string, owner bool, err error) {
	org, owner, err = a.Caller(ctx)
	if err != nil {
		return "", false, err
	}
	if owner {
		return requested, true, nil
	}
	if org == "" {
		return "", false, errs.New(errs.CodeForbidden, "No organisation is selected for this request.")
	}
	if requested != "" && requested != org {
		return "", false, errs.New(errs.CodeForbidden, "Only instance owners may read other organisations' logs.")
	}
	return org, false, nil
}

// visible reports whether a caller scoped to org (owner = all) may see a record of recOrg.
func visible(org string, owner bool, recOrg *string) bool {
	if owner {
		return org == "" || (recOrg != nil && *recOrg == org)
	}
	return recOrg != nil && *recOrg == org
}
