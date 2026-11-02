package imapproxy

import (
	"context"
	"net"
	"strings"

	"github.com/radiobros/emailbridge/internal/crypto/passhash"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Authenticator resolves IMAP proxy LOGIN credentials.
type Authenticator struct {
	Accounts Accounts
	Creds    CredentialStore
	AuthRate *limiter
}

func (a *Authenticator) clientIP(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return remote
	}
	return host
}

// Authenticate verifies username/password for the proxy.
func (a *Authenticator) Authenticate(ctx context.Context, username, password, remote string) (*AccountRef, error) {
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
	cred, err := a.Creds.Get(ctx, acc.ID)
	if err != nil || cred == nil {
		passhash.Burn(password)
		return nil, errAuthFailed
	}
	if !verifyPassword(cred.Hash, password) {
		return nil, errAuthFailed
	}
	upgradeHash(ctx, a.Creds, acc.ID, cred.Hash, password)
	return acc, nil
}

func (a *Authenticator) resolveAccount(ctx context.Context, username string) (*AccountRef, error) {
	if store.IsID(store.PrefixAccount, username) {
		return a.Accounts.ByID(ctx, username)
	}
	return a.Accounts.ByEmail(ctx, strings.ToLower(username))
}

var errAuthFailed = errs.New(errs.CodeInvalidCredentials, "Authentication failed.")
