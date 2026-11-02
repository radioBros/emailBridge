package msa

import (
	"context"

	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// AccountRef is a resolved mailbox account for MSA AUTH.
type AccountRef struct {
	ID             string
	OrganizationID string
	Email          string
	State          string
}

// Accounts resolves accounts by id or email (system / no-tenant lookups).
type Accounts interface {
	ByID(ctx context.Context, accountID string) (*AccountRef, error)
	ByEmail(ctx context.Context, email string) (*AccountRef, error)
}

// APIKeys verifies an API key secret used as an SMTP password.
type APIKeys interface {
	// VerifySecret returns a caller for a raw ebk_… secret, or an error.
	VerifySecret(ctx context.Context, secret string) (*tenancy.Caller, error)
}

// Submitter hands accepted mail to the send pipeline (S6).
type Submitter interface {
	Submit(ctx context.Context, accountID string, req submit.SubmitRequest, key string, includeRaw bool) (*submit.SubmitResult, error)
}

// FeatureGate reports whether protocol.msa is on.
type FeatureGate interface {
	MSAEnabled(ctx context.Context) bool
}

// CredentialStore loads MSA password hashes by account id.
type CredentialStore interface {
	Get(ctx context.Context, accountID string) (*Credential, error)
}

// CredentialRepo is the full credential store used by Service.
type CredentialRepo interface {
	CredentialStore
	Upsert(ctx context.Context, orgID, accountID, hash, by string) error
	Delete(ctx context.Context, accountID string) error
}
