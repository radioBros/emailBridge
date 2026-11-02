package imapproxy

import (
	"context"
	"net"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
)

// AccountRef is a resolved mailbox account for proxy AUTH.
type AccountRef struct {
	ID             string
	OrganizationID string
	Email          string
	State          string
}

// Accounts resolves accounts by id or email.
type Accounts interface {
	ByID(ctx context.Context, accountID string) (*AccountRef, error)
	ByEmail(ctx context.Context, email string) (*AccountRef, error)
}

// IMAPTarget is the real IMAP server to splice to after AUTH.
type IMAPTarget struct {
	Addr               string
	TLS                imapc.TLSMode
	InsecureSkipVerify bool
	Username           string
	Password           string
	AccessToken        string // when set, AUTHENTICATE XOAUTH2 is used
}

// IMAPSource loads decrypted IMAP dial settings for an account.
type IMAPSource interface {
	LoadIMAP(ctx context.Context, accountID string) (*IMAPTarget, error)
}

// DialFunc opens TCP connections (production: egress dialer).
type DialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// FeatureGate reports whether protocol.imapProxy is on.
type FeatureGate interface {
	IMAPProxyEnabled(ctx context.Context) bool
}

// CredentialStore loads proxy password hashes.
type CredentialStore interface {
	Get(ctx context.Context, accountID string) (*Credential, error)
}

// CredentialRepo is the full store used by Service.
type CredentialRepo interface {
	CredentialStore
	Upsert(ctx context.Context, orgID, accountID, hash, by string) error
	Delete(ctx context.Context, accountID string) error
}
