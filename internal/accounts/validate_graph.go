package accounts

import (
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// OutlookOAuthProvider is the oauthApps.provider value for Microsoft (P6.1).
const OutlookOAuthProvider = "outlook"

// GraphAuth describes how a Graph account authenticates (delegated OAuth or app-only).
type GraphAuth struct {
	// OAuthAppProvider is the linked oauthApps.provider (must be "outlook" for delegated).
	OAuthAppProvider string
	// AppOnly is true when the organisation uses client_credentials for this mailbox.
	AppOnly bool
}

// ValidateGraphAccount checks that a provider=graph account has Microsoft OAuth or app-only
// client_credentials. Call when binding OAuth / enabling sync (create may precede connect).
// W22 can extend the sibling Gmail validator the same way.
func ValidateGraphAccount(a *Account, auth GraphAuth) error {
	if a == nil || normalizeProvider(a.Provider) != ProviderGraph {
		return nil
	}
	if auth.AppOnly {
		return nil
	}
	if !a.UsesOAuth() {
		return errs.New(errs.CodeOauthNotConnected,
			"Graph accounts require a Microsoft OAuth connection or app-only client_credentials.")
	}
	p := strings.ToLower(strings.TrimSpace(auth.OAuthAppProvider))
	if p != "" && p != OutlookOAuthProvider {
		return errs.New(errs.CodeValidationFailed,
			"Graph accounts must use an outlook OAuth app.").WithSafe("provider", auth.OAuthAppProvider)
	}
	return nil
}
