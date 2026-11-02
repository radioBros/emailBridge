package accounts

import (
	"context"
	"errors"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// validateDelegatedRef ensures the referenced account is same-org and has OAuth (W54 G24).
// Missing / other-org → 404 before other body semantics leak existence.
func (s *Service) validateDelegatedRef(ctx context.Context, orgID, selfID string, d *DelegatedRef) error {
	if d == nil {
		return nil
	}
	if err := validateDelegatedShape(d); err != nil {
		return err
	}
	if d.Account == selfID {
		return errs.New(errs.CodeDelegatedInvalid, "An account cannot delegate to itself.")
	}
	var doc struct {
		ID    string `bson:"_id"`
		OAuth *struct {
			AppID string `bson:"appId"`
		} `bson:"oauth"`
		Delegated *DelegatedRef `bson:"delegated"`
	}
	err := s.coll().FindOne(ctx, bson.M{"_id": d.Account, "organizationId": orgID}).Decode(&doc)
	if err != nil {
		return errs.New(errs.CodeNotFound, "The delegated account does not exist.")
	}
	if doc.OAuth == nil || doc.OAuth.AppID == "" {
		return errs.New(errs.CodeDelegatedInvalid, "The delegated account has no OAuth connection.")
	}
	if doc.Delegated != nil && doc.Delegated.Account != "" {
		return errs.New(errs.CodeDelegatedInvalid, "Circular delegation is not allowed.")
	}
	d.User = strings.TrimSpace(d.User)
	return nil
}

// refuseDeleteIfDelegatedTarget blocks deleting an account still referenced as delegated.account.
func (s *Service) refuseDeleteIfDelegatedTarget(ctx context.Context, orgID, accountID string) error {
	var ref struct {
		ID string `bson:"_id"`
	}
	err := s.coll().FindOne(ctx, bson.M{
		"organizationId":    orgID,
		"delegated.account": accountID,
		"state":             bson.M{"$ne": StateDeleting},
	}).Decode(&ref)
	if err == nil {
		return errs.New(errs.CodeDelegatedInUse,
			"Another account still delegates through this account; clear the delegation first.").
			WithSafe("referencingAccountId", ref.ID)
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return errs.Wrap(err, errs.CodeInternalError, "accounts.delegated.refs")
	}
	return nil
}

// DelegatedTokenSource returns the account id whose OAuth tokens should be used (self or parent).
func (a *Account) DelegatedTokenSource() string {
	if a != nil && a.Delegated != nil && store.IsID(store.PrefixAccount, a.Delegated.Account) {
		return a.Delegated.Account
	}
	return a.ID
}

// DelegatedUPN returns the shared mailbox UPN when delegated.
func (a *Account) DelegatedUPN() string {
	if a != nil && a.Delegated != nil {
		return strings.TrimSpace(a.Delegated.User)
	}
	return ""
}
