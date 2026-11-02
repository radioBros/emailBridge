package accounts

import (
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/i18n"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/store"
)

var externalIDRe = regexp.MustCompile(`^[A-Za-z0-9._@+-]{1,128}$`)

func validTLS(m string) bool {
	switch m {
	case TLSImplicit, TLSStartTLS, TLSNone:
		return true
	}
	return false
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

func validateEmail(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errs.New(errs.CodeValidationFailed, "email is required.")
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address == "" || !strings.Contains(a.Address, "@") {
		return errs.New(errs.CodeValidationFailed, "email is not a valid address.").WithSafe("email", s)
	}
	return nil
}

func validateExternalID(id string) error {
	if id == "" {
		return nil
	}
	if !externalIDRe.MatchString(id) {
		return errs.New(errs.CodeValidationFailed, "externalId must be 1–128 chars of [A-Za-z0-9._@+-].").WithSafe("externalId", id)
	}
	return nil
}

func validateIMAPInput(in IMAPConfigInput, requirePass bool) error {
	if strings.TrimSpace(in.Host) == "" {
		return errs.New(errs.CodeValidationFailed, "imap.host is required.")
	}
	if !validPort(in.Port) {
		return errs.New(errs.CodeValidationFailed, "imap.port must be 1–65535.").WithSafe("port", strconv.Itoa(in.Port))
	}
	if !validTLS(in.TLS) {
		return errs.New(errs.CodeValidationFailed, "imap.tls must be implicit, starttls or none.").WithSafe("tls", in.TLS)
	}
	if strings.TrimSpace(in.Auth.User) == "" {
		return errs.New(errs.CodeValidationFailed, "imap.auth.user is required.")
	}
	if requirePass && in.Auth.Pass == "" {
		return errs.New(errs.CodeValidationFailed, "imap.auth.pass is required.")
	}
	if len(in.IDName) > 128 {
		return errs.New(errs.CodeValidationFailed, "imap.idName must be ≤ 128 characters.")
	}
	if len(in.IDVersion) > 64 {
		return errs.New(errs.CodeValidationFailed, "imap.idVersion must be ≤ 64 characters.")
	}
	if len(in.IDVendor) > 128 {
		return errs.New(errs.CodeValidationFailed, "imap.idVendor must be ≤ 128 characters.")
	}
	if len(in.IDSupportURL) > 512 {
		return errs.New(errs.CodeValidationFailed, "imap.idSupportUrl must be ≤ 512 characters.")
	}
	return nil
}

func validateNetwork(n *NetworkConfig) error {
	if n == nil {
		return nil
	}
	if n.Proxy != "" {
		u, err := url.Parse(n.Proxy)
		if err != nil || u.Host == "" {
			return errs.New(errs.CodeValidationFailed, "network.proxy must be a socks5 or http(s) URL.")
		}
		switch strings.ToLower(u.Scheme) {
		case "socks5", "socks5h", "http", "https":
		default:
			return errs.New(errs.CodeValidationFailed, "network.proxy scheme must be socks5, socks5h, http or https.")
		}
	}
	if n.LocalAddress != "" {
		if err := validateLocalIP(n.LocalAddress, "network.localAddress"); err != nil {
			return err
		}
	}
	for _, a := range n.LocalAddresses {
		if err := validateLocalIP(a, "network.localAddresses"); err != nil {
			return err
		}
	}
	if err := validatePoolStrategy(n.SMTPStrategy, "network.smtpStrategy"); err != nil {
		return err
	}
	if err := validatePoolStrategy(n.IMAPStrategy, "network.imapStrategy"); err != nil {
		return err
	}
	if n.EHLOName != "" && len(n.EHLOName) > 255 {
		return errs.New(errs.CodeValidationFailed, "network.ehloName must be ≤ 255 characters.")
	}
	return nil
}

func validatePoolStrategy(s, field string) error {
	if s == "" {
		return nil
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s != ippool.StrategyRandom && s != ippool.StrategyRoundRobin && s != ippool.StrategyHashByAccount {
		return errs.New(errs.CodeValidationFailed, field+" must be random, round-robin or hash-by-account.")
	}
	return nil
}

func validateLocalIP(s, field string) error {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	if _, err := netip.ParseAddr(s); err != nil {
		return errs.New(errs.CodeValidationFailed, field+" must be an IP address.").WithSafe("value", s)
	}
	return nil
}

func validateSMTPInput(in SMTPConfigInput, requirePass bool) error {
	if strings.TrimSpace(in.Host) == "" {
		return errs.New(errs.CodeValidationFailed, "smtp.host is required.")
	}
	if !validPort(in.Port) {
		return errs.New(errs.CodeValidationFailed, "smtp.port must be 1–65535.").WithSafe("port", strconv.Itoa(in.Port))
	}
	if !validTLS(in.TLS) {
		return errs.New(errs.CodeValidationFailed, "smtp.tls must be implicit, starttls or none.").WithSafe("tls", in.TLS)
	}
	if strings.TrimSpace(in.Auth.User) == "" {
		return errs.New(errs.CodeValidationFailed, "smtp.auth.user is required.")
	}
	if requirePass && in.Auth.Pass == "" {
		return errs.New(errs.CodeValidationFailed, "smtp.auth.pass is required.")
	}
	if in.Policy != "" && in.Policy != PolicyStandard && in.Policy != PolicyAtMostOnce {
		return errs.New(errs.CodeValidationFailed, "smtp.policy must be standard or atMostOnce.").WithSafe("policy", in.Policy)
	}
	if in.MaxAttempts != nil && *in.MaxAttempts < 1 {
		return errs.New(errs.CodeValidationFailed, "smtp.maxAttempts must be ≥ 1.")
	}
	if in.BackoffBase != "" {
		if _, err := time.ParseDuration(in.BackoffBase); err != nil {
			return errs.New(errs.CodeValidationFailed, "smtp.backoffBase must be a duration.").WithSafe("backoffBase", in.BackoffBase)
		}
	}
	return nil
}

func validateSync(s *SyncConfig) error {
	if s == nil {
		return nil
	}
	for _, p := range s.Paths {
		if err := validatePath(p); err != nil {
			return err
		}
	}
	for _, p := range s.IdlePaths {
		if err := validatePath(p); err != nil {
			return err
		}
	}
	if s.ResyncDelay != "" {
		if _, err := time.ParseDuration(s.ResyncDelay); err != nil {
			return errs.New(errs.CodeValidationFailed, "sync.resyncDelay must be a duration.").WithSafe("resyncDelay", s.ResyncDelay)
		}
	}
	if s.MaxIdle != "" {
		if _, err := time.ParseDuration(s.MaxIdle); err != nil {
			return errs.New(errs.CodeValidationFailed, "sync.maxIdle must be a duration.").WithSafe("maxIdle", s.MaxIdle)
		}
	}
	return nil
}

func validatePath(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return errs.New(errs.CodeValidationFailed, "sync path must not be empty.")
	}
	if p == "*" {
		return nil
	}
	if strings.Contains(p, "\x00") || strings.HasPrefix(p, "/") {
		return errs.New(errs.CodeValidationFailed, "sync path is invalid.").WithSafe("path", p)
	}
	return nil
}

func validateSwitches(sw map[string]any, reg *features.Registry) error {
	if len(sw) == 0 {
		return nil
	}
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	for name, raw := range sw {
		meta, ok := reg.Get(name)
		if !ok {
			return errs.New(errs.CodeSwitchUnknown, "Unknown feature switch.").WithSafe("switch", name)
		}
		if !features.Allows(meta, features.ScopeAccount) {
			return errs.New(errs.CodeValidationFailed, "Switch is not account-scoped.").WithSafe("switch", name)
		}
		switch {
		case meta.Default.IsBool():
			if _, ok := raw.(bool); !ok {
				return errs.New(errs.CodeValidationFailed, "Switch expects a boolean.").WithSafe("switch", name)
			}
		case meta.Default.IsString():
			if _, ok := raw.(string); !ok {
				return errs.New(errs.CodeValidationFailed, "Switch expects a string.").WithSafe("switch", name)
			}
		}
	}
	return nil
}

const (
	ProviderIMAP  = "imap"
	ProviderGmail = "gmail"
	ProviderGraph = "graph"
)

func normalizeProvider(p string) string {
	p = strings.TrimSpace(strings.ToLower(p))
	if p == "" {
		return ProviderIMAP
	}
	return p
}

func validateProvider(p string) error {
	switch normalizeProvider(p) {
	case ProviderIMAP, ProviderGmail, ProviderGraph:
		return nil
	default:
		return errs.New(errs.CodeValidationFailed, "provider must be imap, gmail or graph.").WithSafe("provider", p)
	}
}

// providerAPI is true for Gmail API / Graph accounts (IMAP/SMTP hosts optional).
func providerAPI(p string) bool {
	switch normalizeProvider(p) {
	case ProviderGmail, ProviderGraph:
		return true
	}
	return false
}

func validateCreate(in CreateInput, reg *features.Registry) error {
	if strings.TrimSpace(in.Name) == "" {
		return errs.New(errs.CodeValidationFailed, "name is required.")
	}
	if err := validateEmail(in.Email); err != nil {
		return err
	}
	if err := validateExternalID(in.ExternalID); err != nil {
		return err
	}
	if err := validateProvider(in.Provider); err != nil {
		return err
	}
	api := providerAPI(in.Provider)
	useAuthServer := in.Auth != nil && in.Auth.UseAuthServer
	requirePass := !useAuthServer && in.Delegated == nil
	if api {
		// Hosts optional; when present they must still be well-formed.
		if strings.TrimSpace(in.IMAP.Host) != "" || in.IMAP.Port != 0 || in.IMAP.Auth.User != "" {
			if err := validateIMAPInput(in.IMAP, requirePass && in.IMAP.Auth.Pass != ""); err != nil {
				return err
			}
		}
		if strings.TrimSpace(in.SMTP.Host) != "" || in.SMTP.Port != 0 || in.SMTP.Auth.User != "" {
			if err := validateSMTPInput(in.SMTP, requirePass && in.SMTP.Auth.Pass != ""); err != nil {
				return err
			}
		}
	} else {
		if err := validateIMAPInput(in.IMAP, requirePass); err != nil {
			return err
		}
		if err := validateSMTPInput(in.SMTP, requirePass); err != nil {
			return err
		}
	}
	if err := validateSync(in.Sync); err != nil {
		return err
	}
	if err := validateNetwork(in.Network); err != nil {
		return err
	}
	if err := validateLocale(in.Locale); err != nil {
		return err
	}
	if in.Delegated != nil {
		if err := validateDelegatedShape(in.Delegated); err != nil {
			return err
		}
	}
	return validateSwitches(in.Switches, reg)
}

// validateLocale accepts empty (optional) or any BCP 47 tag whose primary
// subtag is one of the nine product UI codes (it-IT, de-CH, zh-Hans-CN, …).
// Value is stored as given (trimmed); matching reuses internal/i18n.
func validateLocale(locale string) error {
	locale = strings.TrimSpace(locale)
	if locale == "" {
		return nil
	}
	if !i18n.HasSupportedPrimary(locale) {
		return errs.New(errs.CodeValidationFailed, "locale must be one of the supported UI languages.").
			WithSafe("locale", locale)
	}
	return nil
}

func validateDelegatedShape(d *DelegatedRef) error {
	if d == nil {
		return nil
	}
	if !store.IsID(store.PrefixAccount, d.Account) {
		return errs.New(errs.CodeDelegatedInvalid, "delegated.account must be a valid account id.")
	}
	if strings.TrimSpace(d.User) == "" {
		return errs.New(errs.CodeDelegatedInvalid, "delegated.user is required.")
	}
	return nil
}

func defaultSync() SyncConfig {
	return SyncConfig{Paths: []string{"*"}, IdlePaths: []string{"INBOX"}}
}
