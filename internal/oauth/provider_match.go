package oauth

import (
	"strings"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/errs"
)

// providersCompatible reports whether an account provider may connect through the given OAuth app
// provider (W54 G4).
func providersCompatible(accountProvider, appProvider string) bool {
	ap := strings.ToLower(strings.TrimSpace(accountProvider))
	if ap == "" {
		ap = accounts.ProviderIMAP
	}
	app := strings.ToLower(strings.TrimSpace(appProvider))
	switch ap {
	case accounts.ProviderGmail:
		return app == ProviderGmail
	case accounts.ProviderGraph:
		return app == ProviderOutlook
	case accounts.ProviderIMAP:
		switch app {
		case ProviderGmail, ProviderOutlook, ProviderMailru, ProviderGeneric:
			return true
		}
		return false
	default:
		return false
	}
}

func providerMismatch(accountProvider, appProvider string) error {
	ap := strings.ToLower(strings.TrimSpace(accountProvider))
	if ap == "" {
		ap = accounts.ProviderIMAP
	}
	return errs.New(errs.CodeOauthProviderMismatch,
		"The account provider does not match the OAuth app provider.").
		WithSafe("accountProvider", ap).
		WithSafe("appProvider", strings.ToLower(strings.TrimSpace(appProvider)))
}
