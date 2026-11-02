package gateways

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

// ErrAPIGateway is returned by ResolveSMTP when the gateway is an API provider.
var ErrAPIGateway = errors.New("gateways: API gateway (use ResolveAPI)")

// ResolveSMTP decrypts an SMTP gateway for the submit pool. API kinds return ErrAPIGateway.
func (s *Service) ResolveSMTP(ctx context.Context, orgID, id string) (*DialSettings, error) {
	g, err := s.Repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, errs.New(errs.CodeGatewayUnavailable, "The SMTP gateway does not exist or is not available.").
			WithSafe("gatewayId", id)
	}
	if g.IsAPI() {
		return nil, ErrAPIGateway
	}
	if !g.Enabled {
		return nil, errs.New(errs.CodeGatewayUnavailable, "The SMTP gateway is disabled.").
			WithSafe("gatewayId", id)
	}
	pass, err := s.Cipher.DecryptString(ctx, g.Auth.Pass, crypto.AAD{
		Collection: Collection, Field: "auth.pass", DocID: g.ID,
	})
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeInternalError, "gateways.decrypt")
	}
	out := &DialSettings{
		ID: g.ID, OrganizationID: g.OrganizationID, Host: g.Host, Port: g.Port, TLS: g.TLS,
		User: g.Auth.User, Password: pass, EHLOName: g.EHLOName, LocalAddress: g.LocalAddress,
		Proxy: g.Proxy, Policy: g.Policy, IgnoreCertErrors: g.IgnoreCertErrors,
	}
	if g.MaxAttempts != nil {
		out.MaxAttempts = *g.MaxAttempts
	}
	if g.BackoffBase != "" {
		out.BackoffBase, _ = time.ParseDuration(g.BackoffBase)
	}
	return out, nil
}

// ResolveAPI decrypts an API gateway for the submit worker. SMTP / disabled → gateway_unavailable.
func (s *Service) ResolveAPI(ctx context.Context, orgID, id string) (*ResolvedAPI, error) {
	g, err := s.Repo.GetByID(ctx, orgID, id)
	if err != nil {
		return nil, errs.New(errs.CodeGatewayUnavailable, "The API gateway does not exist or is not available.").
			WithSafe("gatewayId", id)
	}
	if !g.IsAPI() {
		return nil, errs.New(errs.CodeGatewayUnavailable, "The gateway is not an API provider.").
			WithSafe("gatewayId", id)
	}
	if !g.Enabled {
		return nil, errs.New(errs.CodeGatewayUnavailable, "The API gateway is disabled.").
			WithSafe("gatewayId", id)
	}
	cfg, err := s.decryptProviderConfig(ctx, g)
	if err != nil {
		return nil, err
	}
	out := &ResolvedAPI{
		GatewayID: g.ID, Kind: g.EffectiveKind(), Config: cfg, Policy: policyOr(g.Policy),
	}
	if g.MaxAttempts != nil {
		out.MaxAttempts = *g.MaxAttempts
	}
	if g.BackoffBase != "" {
		out.BackoffBase, _ = time.ParseDuration(g.BackoffBase)
	}
	return out, nil
}

func (s *Service) decryptProviderConfig(ctx context.Context, g *Gateway) (ProviderConfig, error) {
	cfg := ProviderConfig{Kind: g.EffectiveKind()}
	if g.API != nil {
		cfg.Region = g.API.Region
		cfg.Domain = g.API.Domain
		cfg.BaseURL = g.API.BaseURL
		cfg.From = g.API.From
		cfg.Extra = g.API.Extra
	}
	if g.APICreds == nil {
		return cfg, nil
	}
	dec := func(field string, enc crypto.EncryptedString) (string, error) {
		if enc.IsZero() {
			return "", nil
		}
		return s.Cipher.DecryptString(ctx, enc, crypto.AAD{
			Collection: Collection, Field: "apiCreds." + field, DocID: g.ID,
		})
	}
	var err error
	if cfg.AccessKey, err = dec("accessKey", g.APICreds.AccessKey); err != nil {
		return cfg, errs.Wrap(err, errs.CodeInternalError, "gateways.decrypt")
	}
	if cfg.SecretKey, err = dec("secretKey", g.APICreds.SecretKey); err != nil {
		return cfg, errs.Wrap(err, errs.CodeInternalError, "gateways.decrypt")
	}
	if cfg.APIKey, err = dec("apiKey", g.APICreds.APIKey); err != nil {
		return cfg, errs.Wrap(err, errs.CodeInternalError, "gateways.decrypt")
	}
	return cfg, nil
}

func (s *Service) decryptWebhookSecret(ctx context.Context, g *Gateway) (string, error) {
	if g.APICreds == nil || g.APICreds.WebhookSecret.IsZero() {
		// Mailgun may fall back to apiKey for signing.
		if g.EffectiveKind() == KindMailgun && g.APICreds != nil && !g.APICreds.APIKey.IsZero() {
			return s.Cipher.DecryptString(ctx, g.APICreds.APIKey, crypto.AAD{
				Collection: Collection, Field: "apiCreds.apiKey", DocID: g.ID,
			})
		}
		return "", nil
	}
	return s.Cipher.DecryptString(ctx, g.APICreds.WebhookSecret, crypto.AAD{
		Collection: Collection, Field: "apiCreds.webhookSecret", DocID: g.ID,
	})
}

// Send delivers mail through the resolved API provider using the egress HTTP client.
func (s *Service) Send(ctx context.Context, r *ResolvedAPI, msg OutboundMail) (SendResult, error) {
	if r == nil {
		return SendResult{}, errs.New(errs.CodeGatewayUnavailable, "API gateway is not resolved.")
	}
	p, ok := ProviderFor(r.Kind)
	if !ok {
		return SendResult{}, errs.New(errs.CodeGatewayUnavailable, "Unknown API gateway kind.").
			WithSafe("kind", r.Kind)
	}
	client := s.httpClient(60 * time.Second)
	res, err := p.Send(ctx, client, r.Config, msg)
	if err != nil {
		return SendResult{}, classifySendErr(err)
	}
	return res, nil
}

func (s *Service) httpClient(timeout time.Duration) *http.Client {
	if s != nil && s.HTTP != nil {
		return s.HTTP
	}
	if s != nil && s.Egress != nil {
		return s.Egress.HTTPClient(timeout)
	}
	return &http.Client{Timeout: timeout}
}

func classifySendErr(err error) error {
	if err == nil {
		return nil
	}
	var e *errs.E
	if errors.As(err, &e) {
		return e
	}
	msg := err.Error()
	// Providers embed "kind=… code=…" via apiErr / ses path.
	kind := errs.KindTemporary
	code := errs.CodeSMTPTemporary
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "kind=auth") || strings.Contains(low, "code=smtp_auth_failed"):
		kind, code = errs.KindAuth, errs.CodeSMTPAuthFailed
	case strings.Contains(low, "kind=rate_limited") || strings.Contains(low, "code=smtp_rate_limited"):
		kind, code = errs.KindRateLimited, errs.CodeSMTPRateLimited
	case strings.Contains(low, "kind=permanent") || strings.Contains(low, "code=smtp_permanent"):
		kind, code = errs.KindPermanent, errs.CodeSMTPPermanent
	case strings.Contains(low, "kind=temporary") || strings.Contains(low, "code=smtp_temporary"):
		kind, code = errs.KindTemporary, errs.CodeSMTPTemporary
	}
	return &errs.E{Kind: kind, Code: code, Msg: "API gateway send failed", Err: err, Op: "gateways.Send"}
}
