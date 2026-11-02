package msa

import (
	"context"
	"net"
	"strings"

	"github.com/radiobros/emailbridge/internal/auth"
	"github.com/radiobros/emailbridge/internal/crypto/passhash"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Authed is a successful MSA authentication.
type Authed struct {
	Account *AccountRef
	Caller  tenancy.Caller
}

// Authenticator resolves SMTP AUTH credentials.
type Authenticator struct {
	Accounts Accounts
	Creds    CredentialStore
	APIKeys  APIKeys
	AuthRate *limiter
}

func (a *Authenticator) clientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

// Authenticate verifies username/password. remote is host:port of the client.
func (a *Authenticator) Authenticate(ctx context.Context, username, password, remote string) (*Authed, error) {
	ip := a.clientIP(remote)
	if a.AuthRate != nil && !a.AuthRate.allow(ip) {
		return nil, errs.New(errs.CodeRateLimited, "Too many authentication attempts.")
	}
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return nil, errAuthFailed
	}
	acc, err := a.resolveAccount(ctx, username)
	if err != nil || acc == nil {
		passhash.Burn(password)
		return nil, errAuthFailed
	}
	if acc.State == "disabled" || acc.State == "deleting" {
		passhash.Burn(password)
		return nil, errAuthFailed
	}

	if strings.HasPrefix(password, auth.APIKeyPrefix) && a.APIKeys != nil {
		caller, err := a.APIKeys.VerifySecret(ctx, password)
		if err != nil || caller == nil {
			return nil, errAuthFailed
		}
		if caller.AccountID == "" || caller.AccountID != acc.ID {
			return nil, errAuthFailed
		}
		if caller.OrganizationID != acc.OrganizationID {
			return nil, errAuthFailed
		}
		if !caller.AllowsOn(tenancy.PermSend, acc.ID) {
			return nil, errAuthFailed
		}
		return &Authed{Account: acc, Caller: *caller}, nil
	}

	cred, err := a.Creds.Get(ctx, acc.ID)
	if err != nil || cred == nil {
		passhash.Burn(password)
		return nil, errAuthFailed
	}
	if !verifyPassword(cred.Hash, password) {
		return nil, errAuthFailed
	}
	upgradeHash(ctx, a.Creds, acc.ID, cred.Hash, password)
	return &Authed{
		Account: acc,
		Caller: tenancy.Caller{
			Kind: tenancy.CallerSystem, OrganizationID: acc.OrganizationID, AccountID: acc.ID,
		},
	}, nil
}

func (a *Authenticator) resolveAccount(ctx context.Context, username string) (*AccountRef, error) {
	if store.IsID(store.PrefixAccount, username) {
		return a.Accounts.ByID(ctx, username)
	}
	return a.Accounts.ByEmail(ctx, strings.ToLower(username))
}

var errAuthFailed = errs.New(errs.CodeInvalidCredentials, "Authentication failed.")
