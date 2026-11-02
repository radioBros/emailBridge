package gateways

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func normalizeKind(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		return KindSMTP
	}
	return k
}

func isAPIKind(k string) bool {
	_, ok := ProviderFor(k)
	return ok
}

func validateCreate(in CreateInput) error {
	kind := normalizeKind(in.Kind)
	if kind != KindSMTP && !isAPIKind(kind) {
		return errs.New(errs.CodeValidationFailed, "kind must be smtp or a known API provider.")
	}
	if kind == KindSMTP {
		return validateSMTPCreate(in)
	}
	return validateAPICreate(kind, in)
}

func validateSMTPCreate(in CreateInput) error {
	if in.API != nil || in.Credentials != nil {
		return errs.New(errs.CodeValidationFailed, "api and credentials are not allowed on SMTP gateways.")
	}
	g := &Gateway{
		Kind: KindSMTP, Name: in.Name, Host: in.Host, Port: in.Port, TLS: in.TLS,
		Auth: Auth{User: in.Auth.User}, EHLOName: in.EHLOName, LocalAddress: in.LocalAddress,
		Proxy: in.Proxy, Policy: in.Policy, MaxAttempts: in.MaxAttempts, BackoffBase: in.BackoffBase,
	}
	if strings.TrimSpace(in.Auth.Pass) == "" {
		return errs.New(errs.CodeValidationFailed, "auth.pass is required.")
	}
	return validateSMTPGateway(g)
}

func validateAPICreate(kind string, in CreateInput) error {
	if strings.TrimSpace(in.Host) != "" || in.Port != 0 || in.TLS != "" ||
		in.Auth.User != "" || in.Auth.Pass != "" || in.EHLOName != "" ||
		in.LocalAddress != "" || in.Proxy != "" || in.IgnoreCertErrors {
		return errs.New(errs.CodeValidationFailed, "SMTP fields are not allowed on API gateways.")
	}
	if strings.TrimSpace(in.Name) == "" || len(in.Name) > 200 {
		return errs.New(errs.CodeValidationFailed, "name must be 1 to 200 characters.")
	}
	if err := validateAPIConfig(kind, in.API); err != nil {
		return err
	}
	if err := validateAPICredsRequired(kind, in.Credentials); err != nil {
		return err
	}
	return validatePolicyFields(in.Policy, in.MaxAttempts, in.BackoffBase)
}

func validateSMTPGateway(g *Gateway) error {
	if strings.TrimSpace(g.Name) == "" || len(g.Name) > 200 {
		return errs.New(errs.CodeValidationFailed, "name must be 1 to 200 characters.")
	}
	if strings.TrimSpace(g.Host) == "" {
		return errs.New(errs.CodeValidationFailed, "host is required.")
	}
	if g.Port < 1 || g.Port > 65535 {
		return errs.New(errs.CodeValidationFailed, "port must be between 1 and 65535.")
	}
	switch g.TLS {
	case TLSImplicit, TLSStartTLS, TLSNone:
	default:
		return errs.New(errs.CodeValidationFailed, "tls must be implicit, starttls or none.")
	}
	if strings.TrimSpace(g.Auth.User) == "" {
		return errs.New(errs.CodeValidationFailed, "auth.user is required.")
	}
	if g.Proxy != "" {
		u, err := url.Parse(g.Proxy)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return errs.New(errs.CodeValidationFailed, "proxy must be a socks5 or http URL.")
		}
		switch strings.ToLower(u.Scheme) {
		case "socks5", "socks5h", "http", "https":
		default:
			return errs.New(errs.CodeValidationFailed, "proxy scheme must be socks5 or http.")
		}
	}
	return validatePolicyFields(g.Policy, g.MaxAttempts, g.BackoffBase)
}

func validateAPIConfig(kind string, api *APIConfig) error {
	if api == nil {
		api = &APIConfig{}
	}
	if kind == KindMailgun && strings.TrimSpace(api.Domain) == "" {
		return errs.New(errs.CodeValidationFailed, "api.domain is required for mailgun.")
	}
	if api.BaseURL != "" {
		u, err := url.Parse(api.BaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errs.New(errs.CodeValidationFailed, "api.baseUrl must be an http(s) URL.")
		}
	}
	return nil
}

func validateAPICredsRequired(kind string, c *CredentialsInput) error {
	if c == nil {
		return errs.New(errs.CodeValidationFailed, "credentials are required.")
	}
	switch kind {
	case KindSES:
		if strings.TrimSpace(c.AccessKey) == "" || strings.TrimSpace(c.SecretKey) == "" {
			return errs.New(errs.CodeValidationFailed, "credentials.accessKey and credentials.secretKey are required for ses.")
		}
	default:
		if strings.TrimSpace(c.APIKey) == "" {
			return errs.New(errs.CodeValidationFailed, "credentials.apiKey is required.")
		}
	}
	return nil
}

func validatePolicyFields(policy string, maxAttempts *int, backoff string) error {
	if policy != "" && policy != PolicyStandard && policy != PolicyAtMostOnce {
		return errs.New(errs.CodeValidationFailed, "policy must be standard or atMostOnce.")
	}
	if backoff != "" {
		if _, err := time.ParseDuration(backoff); err != nil {
			return errs.New(errs.CodeValidationFailed, "backoffBase must be a duration.")
		}
	}
	if maxAttempts != nil && (*maxAttempts < 1 || *maxAttempts > 100) {
		return errs.New(errs.CodeValidationFailed, "maxAttempts must be between 1 and 100.")
	}
	return nil
}

func checkBaseURLEgress(ctx context.Context, d *egress.Dialer, baseURL string) error {
	if d == nil || strings.TrimSpace(baseURL) == "" {
		return nil
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "api.baseUrl must be an http(s) URL.")
	}
	if err := d.CheckURLHost(ctx, u.Hostname(), egress.HTTP); err != nil {
		return errs.New(errs.CodeValidationFailed, "api.baseUrl host is blocked by egress policy.").
			WithSafe("host", u.Hostname())
	}
	return nil
}

func refuseSMTPFieldsOnAPI(in PatchInput) error {
	if in.Host != nil || in.Port != nil || in.TLS != nil || in.Auth != nil ||
		in.EHLOName != nil || in.LocalAddress != nil || in.Proxy != nil || in.IgnoreCertErrors != nil {
		return errs.New(errs.CodeValidationFailed, "SMTP fields are not allowed on API gateways.")
	}
	return nil
}

func refuseAPIFieldsOnSMTP(in PatchInput) error {
	if in.API != nil || in.Credentials != nil {
		return errs.New(errs.CodeValidationFailed, "api and credentials are not allowed on SMTP gateways.")
	}
	return nil
}
