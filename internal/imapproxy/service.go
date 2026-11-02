package imapproxy

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const minPasswordLen = 12

// Service manages IMAP proxy credentials.
type Service struct {
	Repo CredentialRepo
	// OwnsAccount reports whether accountID belongs to orgID. Credentials are keyed by account ID alone, so every
	// operation checks ownership first: without it an owner of one organisation could set a password on another
	// organisation's account and send / read as it. Nil = refuse (fail closed).
	OwnsAccount func(ctx context.Context, orgID, accountID string) (bool, error)
}

// requireOwnAccount refuses an account of another organisation with the same answer as a missing one.
func (s *Service) requireOwnAccount(ctx context.Context, accountID string) error {
	if !store.IsID(store.PrefixAccount, accountID) {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return store.ErrNoTenant
	}
	if s.OwnsAccount == nil {
		return errs.New(errs.CodeInternalError, "The account ownership check is not configured.")
	}
	own, err := s.OwnsAccount(ctx, org, accountID)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "imapproxy.account_owner")
	}
	if !own {
		return errs.New(errs.CodeNotFound, "The account does not exist.")
	}
	return nil
}

// PasswordStatus is the GET body.
type PasswordStatus struct {
	Set       bool       `json:"set" doc:"True when a password is set for the account." example:"true"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty" doc:"When the password was last set (absent when none is set)." example:"2026-10-25T16:20:00Z"`
}

// Status returns whether a proxy password is set.
func (s *Service) Status(ctx context.Context, accountID string) (*PasswordStatus, error) {
	if err := tenancy.RequireReadAccount(ctx, "accounts", accountID); err != nil {
		return nil, err
	}
	if err := s.requireOwnAccount(ctx, accountID); err != nil {
		return nil, err
	}
	c, err := s.Repo.Get(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := &PasswordStatus{Set: c != nil}
	if c != nil {
		t := c.UpdatedAt
		out.UpdatedAt = &t
	}
	return out, nil
}

// SetPassword stores a new proxy password.
func (s *Service) SetPassword(ctx context.Context, accountID, password string) (*PasswordStatus, error) {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsWrite, accountID); err != nil {
		return nil, err
	}
	if err := s.requireOwnAccount(ctx, accountID); err != nil {
		return nil, err
	}
	org, ok := store.OrganizationFrom(ctx)
	if !ok {
		return nil, store.ErrNoTenant
	}
	password = strings.TrimSpace(password)
	if utf8.RuneCountInString(password) < minPasswordLen {
		return nil, errs.New(errs.CodeValidationFailed, "Password must be at least 12 characters.")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "imapproxy.hash")
	}
	by := ""
	if c, ok := tenancy.CallerFrom(ctx); ok {
		by = c.Actor()
	}
	if err := s.Repo.Upsert(ctx, org, accountID, hash, by); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	return &PasswordStatus{Set: true, UpdatedAt: &now}, nil
}

// ClearPassword deletes the proxy password.
func (s *Service) ClearPassword(ctx context.Context, accountID string) error {
	if err := tenancy.RequireAccount(ctx, tenancy.PermAccountsWrite, accountID); err != nil {
		return err
	}
	if err := s.requireOwnAccount(ctx, accountID); err != nil {
		return err
	}
	return s.Repo.Delete(ctx, accountID)
}
