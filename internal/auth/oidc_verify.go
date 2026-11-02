package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type oidcClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Subject       string `json:"sub"`
	Raw           map[string]any
}

func (s *Service) oidcCtx(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, s.httpClient())
}

func (s *Service) oidcProvider(ctx context.Context, issuer string) (*oidc.Provider, error) {
	return oidc.NewProvider(s.oidcCtx(ctx), issuer)
}

func (s *Service) verifyIDToken(ctx context.Context, issuer, clientID, rawIDToken, nonce string) (*oidcClaims, error) {
	ctx = s.oidcCtx(ctx)
	provider, err := s.oidcProvider(ctx, issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: clientID})
	tok, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, fmt.Errorf("id_token: %w", err)
	}
	if nonce != "" && tok.Nonce != nonce {
		return nil, fmt.Errorf("id_token: nonce mismatch")
	}
	var claims oidcClaims
	if err := tok.Claims(&claims); err != nil {
		return nil, fmt.Errorf("id_token claims: %w", err)
	}
	raw := map[string]any{}
	if err := tok.Claims(&raw); err == nil {
		claims.Raw = raw
	}
	claims.Email = strings.TrimSpace(strings.ToLower(claims.Email))
	return &claims, nil
}

func (s *Service) exchangeOIDCCode(ctx context.Context, p *OIDCProvider, secret, code, verifier, redirectURL string) (*oauth2.Token, error) {
	ctx = s.oidcCtx(ctx)
	provider, err := s.oidcProvider(ctx, p.IssuerURL)
	if err != nil {
		return nil, err
	}
	endpoint := provider.Endpoint()
	// Pin auth style so oauth2 does not probe twice (second try would burn a one-time code).
	endpoint.AuthStyle = oauth2.AuthStyleInHeader
	cfg := &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: secret,
		RedirectURL:  redirectURL,
		Scopes:       p.Scopes,
		Endpoint:     endpoint,
	}
	exCtx := context.WithValue(ctx, oauth2.HTTPClient, s.httpClient())
	return cfg.Exchange(exCtx, code, oauth2.VerifierOption(verifier))
}

func idTokenFromOauthToken(tok *oauth2.Token) (string, error) {
	raw, ok := tok.Extra("id_token").(string)
	if !ok || raw == "" {
		return "", fmt.Errorf("token response missing id_token")
	}
	return raw, nil
}

func groupsFromClaims(claims *oidcClaims, claimName string) []string {
	if claims == nil || claimName == "" || claims.Raw == nil {
		return nil
	}
	v, ok := claims.Raw[claimName]
	if !ok {
		return nil
	}
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return nil
		}
		var ss []string
		if json.Unmarshal(b, &ss) == nil {
			return ss
		}
		return nil
	}
}
