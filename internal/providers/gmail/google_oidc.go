package gmail

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// googleIssuer is the issuer of the OIDC tokens Pub/Sub push subscriptions attach.
const googleIssuer = "https://accounts.google.com"

// GoogleVerifier verifies the OIDC token Google Pub/Sub attaches to push requests: signature (Google JWKS),
// issuer, audience (our callback URL) and expiry; with ServiceAccount set, also that the token was issued
// for the push subscription's service account (email claim, verified). Discovery is retried after a failure
// instead of being cached as an error.
type GoogleVerifier struct {
	ServiceAccount string
	HTTP           *http.Client

	mu   sync.Mutex
	prov *oidc.Provider
}

// Verify implements OIDCVerifier.
func (v *GoogleVerifier) Verify(ctx context.Context, rawToken, audience string) error {
	if rawToken == "" || audience == "" {
		return errors.New("gmail push: missing token or audience")
	}
	client := v.HTTP
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	octx := oidc.ClientContext(ctx, client)
	prov, err := v.provider(octx)
	if err != nil {
		return err
	}
	tok, err := prov.Verifier(&oidc.Config{ClientID: audience}).Verify(octx, rawToken)
	if err != nil {
		return err
	}
	if v.ServiceAccount == "" {
		return nil
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := tok.Claims(&claims); err != nil {
		return err
	}
	if !claims.EmailVerified || !strings.EqualFold(claims.Email, v.ServiceAccount) {
		return errors.New("gmail push: token not issued for the configured service account")
	}
	return nil
}

func (v *GoogleVerifier) provider(ctx context.Context) (*oidc.Provider, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.prov != nil {
		return v.prov, nil
	}
	p, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, err
	}
	v.prov = p
	return p, nil
}
