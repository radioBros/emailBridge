package config

import (
	"encoding/base64"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// LoadFromOS loads configuration from the real process environment.
func LoadFromOS() (*Config, *Report, error) {
	return Load(osEnv{})
}

// Load reads configuration from env using struct tags on Config.
// Precedence: built-in default → environment / <KEY>_FILE.
func Load(env Env) (*Config, *Report, error) {
	cfg := &Config{}
	metas := collectMeta(cfg)
	report := &Report{Sources: make(map[string]Source, len(metas))}
	known := make(map[string]struct{}, len(metas)*2)
	secretKeys := map[string]struct{}{}
	var problems []string

	for _, m := range metas {
		known[m.Key] = struct{}{}
		known[m.Key+"_FILE"] = struct{}{}
		if m.Secret {
			secretKeys[m.Key] = struct{}{}
		}
	}

	for _, m := range metas {
		raw, src, err := resolveValue(env, m.Key)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if raw == "" && m.Default != "" {
			raw = m.Default
			src = SourceDefault
		}
		if raw == "" && src == "" {
			src = SourceDefault
		}
		report.Sources[m.Key] = src
		if raw == "" {
			continue
		}
		if err := setField(m, raw); err != nil {
			problems = append(problems, formatParseErr(m.Key, err, m.Secret))
		}
	}

	applyComputedDefaults(cfg, report)

	problems = append(problems, validateLoaded(cfg, metas, secretKeys)...)

	report.Warnings = append(report.Warnings, unknownWarnings(env, known)...)

	if len(problems) > 0 {
		return cfg, report, &ValidationError{Problems: problems}
	}
	return cfg, report, nil
}

func resolveValue(env Env, key string) (string, Source, error) {
	fileKey := key + "_FILE"
	_, hasEnv := env.LookupEnv(key)
	filePath, hasFile := env.LookupEnv(fileKey)
	if hasEnv && hasFile {
		return "", "", fmt.Errorf("%s: both %s and %s are set; use only one", key, key, fileKey)
	}
	if hasFile {
		b, err := env.ReadFile(filePath)
		if err != nil {
			return "", "", fmt.Errorf("%s: reading %s (%s): %w", key, fileKey, filePath, err)
		}
		return trimFileValue(b), SourceFile, nil
	}
	if hasEnv {
		v, _ := env.LookupEnv(key)
		return v, SourceEnv, nil
	}
	return "", "", nil
}

func formatParseErr(key string, err error, secret bool) string {
	if secret {
		return fmt.Sprintf("%s: invalid value (secret; not shown): %v", key, errShape(err))
	}
	return fmt.Sprintf("%s: %v", key, err)
}

func errShape(err error) string {
	// Never include the original err text if it might contain the value;
	// parsers for secret types already avoid echoing the value.
	msg := err.Error()
	if strings.Contains(msg, "base64") || strings.Contains(msg, "32 bytes") ||
		strings.Contains(msg, "kid") || strings.Contains(msg, "key ring") ||
		strings.Contains(msg, "empty") {
		return msg
	}
	return "see expected format in S9"
}

func setField(m fieldMeta, raw string) error {
	switch m.Kind {
	case kindString:
		*(m.Field.(*string)) = raw
	case kindInt:
		n, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return fmt.Errorf("want integer, got %q", raw)
		}
		*(m.Field.(*int)) = n
	case kindInt64:
		n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			return fmt.Errorf("want integer, got %q", raw)
		}
		*(m.Field.(*int64)) = n
	case kindBool:
		b, err := ParseBool(raw)
		if err != nil {
			return err
		}
		*(m.Field.(*bool)) = b
	case kindFloat64:
		f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return fmt.Errorf("want float, got %q", raw)
		}
		*(m.Field.(*float64)) = f
	case kindDuration:
		d, err := ParseDuration(raw)
		if err != nil {
			return err
		}
		*(m.Field.(*time.Duration)) = d
	case kindSize:
		sz, err := ParseSize(raw)
		if err != nil {
			return err
		}
		*(m.Field.(*Size)) = sz
	case kindStringSlice:
		*(m.Field.(*[]string)) = ParseStringList(raw)
	case kindPrefixSlice:
		p, err := ParseCIDRList(raw)
		if err != nil {
			return err
		}
		*(m.Field.(*[]netip.Prefix)) = p
	case kindRate:
		r, err := ParseRate(raw)
		if err != nil {
			return err
		}
		*(m.Field.(*Rate)) = r
	case kindKeyRing:
		kr, err := ParseKeyRing(raw)
		if err != nil {
			return err
		}
		*(m.Field.(*KeyRing)) = kr
	default:
		return fmt.Errorf("unsupported field kind")
	}
	if len(m.OneOf) > 0 {
		ok := false
		val := raw
		if m.Kind == kindString {
			val = *(m.Field.(*string))
		}
		for _, o := range m.OneOf {
			if val == o {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("want one of %s, got %q", strings.Join(m.OneOf, "|"), val)
		}
	}
	return nil
}

func collectMeta(cfg *Config) []fieldMeta {
	var out []fieldMeta
	walkStruct(reflect.ValueOf(cfg).Elem(), &out)
	return out
}

func walkStruct(v reflect.Value, out *[]fieldMeta) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := v.Field(i)
		if sf.Type.Kind() == reflect.Struct && sf.Type != reflect.TypeOf(time.Duration(0)) &&
			sf.Type != reflect.TypeOf(Rate{}) && sf.Type != reflect.TypeOf(Size(0)) {
			// KeyRing is a slice type, not struct
			if sf.Tag.Get("env") == "" {
				walkStruct(fv, out)
				continue
			}
		}
		key := sf.Tag.Get("env")
		if key == "" {
			continue
		}
		m := fieldMeta{
			Key:      key,
			Default:  sf.Tag.Get("default"),
			Secret:   sf.Tag.Get("secret") == "true",
			Runtime:  sf.Tag.Get("runtime") == "true",
			Required: sf.Tag.Get("required") == "true",
		}
		if o := sf.Tag.Get("oneof"); o != "" {
			m.OneOf = strings.Split(o, "|")
		}
		ptr := fv.Addr().Interface()
		switch sf.Type {
		case reflect.TypeOf(""):
			m.Kind = kindString
			m.Field = ptr.(*string)
		case reflect.TypeOf(0):
			m.Kind = kindInt
			m.Field = ptr.(*int)
		case reflect.TypeOf(int64(0)):
			m.Kind = kindInt64
			m.Field = ptr.(*int64)
		case reflect.TypeOf(false):
			m.Kind = kindBool
			m.Field = ptr.(*bool)
		case reflect.TypeOf(float64(0)):
			m.Kind = kindFloat64
			m.Field = ptr.(*float64)
		case reflect.TypeOf(time.Duration(0)):
			m.Kind = kindDuration
			m.Field = ptr.(*time.Duration)
		case reflect.TypeOf(Size(0)):
			m.Kind = kindSize
			m.Field = ptr.(*Size)
		case reflect.TypeOf([]string(nil)):
			m.Kind = kindStringSlice
			m.Field = ptr.(*[]string)
		case reflect.TypeOf([]netip.Prefix(nil)):
			m.Kind = kindPrefixSlice
			m.Field = ptr.(*[]netip.Prefix)
		case reflect.TypeOf(Rate{}):
			m.Kind = kindRate
			m.Field = ptr.(*Rate)
		case reflect.TypeOf(KeyRing(nil)):
			m.Kind = kindKeyRing
			m.Field = ptr.(*KeyRing)
		default:
			continue
		}
		*out = append(*out, m)
	}
}

func applyComputedDefaults(cfg *Config, report *Report) {
	if cfg.Process.NodeID == "" {
		h, err := os.Hostname()
		if err != nil {
			h = "unknown"
		}
		cfg.Process.NodeID = h
		if report.Sources["EB_NODE_ID"] == "" || report.Sources["EB_NODE_ID"] == SourceDefault {
			report.Sources["EB_NODE_ID"] = SourceDefault
		}
	}
	if cfg.HTTP.CallbackURL == "" {
		cfg.HTTP.CallbackURL = cfg.HTTP.PublicURL
		if report.Sources["EB_CALLBACK_URL"] == "" || report.Sources["EB_CALLBACK_URL"] == SourceDefault {
			report.Sources["EB_CALLBACK_URL"] = SourceDefault
		}
	}
	host := hostOfURL(cfg.HTTP.PublicURL)
	if cfg.Auth.CookieDomain == "" {
		cfg.Auth.CookieDomain = host
	}
	if cfg.Auth.WebAuthnRPID == "" {
		cfg.Auth.WebAuthnRPID = host
		if _, err := netip.ParseAddr(host); err == nil {
			// A WebAuthn relying-party ID must be a domain: an IP public URL cannot use passkeys at all, and the
			// passkey library refuses an IP at start. Fall back to localhost so the node starts; passwords and
			// TOTP work, passkeys need EB_PUBLIC_URL (or EB_AUTH_WEBAUTHN_RP_ID) with a domain name.
			cfg.Auth.WebAuthnRPID = "localhost"
			report.Warnings = append(report.Warnings, fmt.Sprintf(
				"EB_PUBLIC_URL host %s is an IP address: passkeys are unavailable until the public URL uses a domain name (e.g. localhost)", host))
		}
	}
	if cfg.Keys.Active == "" && len(cfg.Keys.Local) > 0 {
		cfg.Keys.Active = cfg.Keys.Local[0].Kid
	}
	if cfg.Log.Dir == "" {
		cfg.Log.Dir = strings.TrimRight(cfg.Process.DataDir, "/") + "/logs"
	}
	if cfg.Log.SentryEnvironment == "" {
		cfg.Log.SentryEnvironment = cfg.Process.Env
	}
	if cfg.Lease.LeaseRenew == 0 && cfg.Lease.LeaseTTL > 0 {
		cfg.Lease.LeaseRenew = cfg.Lease.LeaseTTL / 3
	}
}

func hostOfURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	return h
}

func validateLoaded(cfg *Config, metas []fieldMeta, secretKeys map[string]struct{}) []string {
	var problems []string
	for _, m := range metas {
		if !m.Required {
			continue
		}
		if isZero(m) {
			problems = append(problems, fmt.Sprintf("%s: required", m.Key))
		}
	}
	if cfg.Keys.Provider == "local" && len(cfg.Keys.Local) == 0 {
		problems = append(problems, "EB_KEYS_LOCAL: required when EB_KEYS_PROVIDER=local")
	}
	if cfg.Stores.NATSMode == "external" && cfg.Stores.NATSURL == "" {
		problems = append(problems, "EB_NATS_URL: required when EB_NATS_MODE=external")
	}
	if cfg.HTTP.PublicURL != "" {
		if _, err := url.ParseRequestURI(cfg.HTTP.PublicURL); err != nil {
			problems = append(problems, fmt.Sprintf("EB_PUBLIC_URL: invalid URL: %v", err))
		}
	}
	if cfg.HTTP.CallbackURL != "" {
		if _, err := url.ParseRequestURI(cfg.HTTP.CallbackURL); err != nil {
			problems = append(problems, fmt.Sprintf("EB_CALLBACK_URL: invalid URL: %v", err))
		}
	}
	if cfg.Auth.Secret != "" {
		if err := validateAuthSecret(cfg.Auth.Secret); err != nil {
			_ = secretKeys // silence — never print the value
			problems = append(problems, "EB_AUTH_SECRET: "+err.Error())
		}
	}
	if !cfg.Auth.CookieSecure && cfg.Process.Env != "development" {
		problems = append(problems, "EB_AUTH_COOKIE_SECURE: false only allowed when EB_ENV=development")
	}
	if _, err := netip.ParseAddr(cfg.Auth.WebAuthnRPID); err == nil {
		problems = append(problems, "EB_AUTH_WEBAUTHN_RP_ID: must be a domain name, not an IP address")
	}
	if cfg.SysMail.SMTPTLS == "none" && cfg.Process.Env != "development" {
		problems = append(problems, "EB_SYSMAIL_SMTP_TLS: none only allowed when EB_ENV=development")
	}
	problems = append(problems, validatePositive(cfg)...)
	return problems
}

// validateAuthSecret accepts standard base64 of exactly 32 bytes (recommended)
// or a raw 32-byte string.
func validateAuthSecret(s string) error {
	if len(s) == 32 {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("must be base64 of 32 bytes (recommended) or a raw 32-byte string")
	}
	if len(raw) != 32 {
		return fmt.Errorf("must decode to exactly 32 bytes, got %d", len(raw))
	}
	return nil
}

func isZero(m fieldMeta) bool {
	switch m.Kind {
	case kindString:
		return *(m.Field.(*string)) == ""
	case kindInt:
		return *(m.Field.(*int)) == 0
	case kindInt64:
		return *(m.Field.(*int64)) == 0
	case kindBool:
		return false // bool required doesn't make sense; treat as set
	case kindFloat64:
		return false
	case kindDuration:
		return *(m.Field.(*time.Duration)) == 0
	case kindSize:
		return *(m.Field.(*Size)) == 0
	case kindStringSlice:
		return len(*(m.Field.(*[]string))) == 0
	case kindPrefixSlice:
		return len(*(m.Field.(*[]netip.Prefix))) == 0
	case kindRate:
		return (*(m.Field.(*Rate))).Count == 0
	case kindKeyRing:
		return len(*(m.Field.(*KeyRing))) == 0
	}
	return true
}

func validatePositive(cfg *Config) []string {
	var p []string
	checkDur := func(key string, d time.Duration) {
		if d < 0 {
			p = append(p, key+": must be non-negative duration")
		}
	}
	checkSize := func(key string, s Size) {
		if s < 0 {
			p = append(p, key+": must be non-negative size")
		}
	}
	checkDur("EB_SHUTDOWN_GRACE", cfg.Process.ShutdownGrace)
	checkDur("EB_HTTP_REQUEST_TIMEOUT", cfg.HTTP.RequestTimeout)
	checkDur("EB_HTTP_REQUEST_TIMEOUT_MAX", cfg.HTTP.RequestTimeoutMax)
	checkSize("EB_HTTP_MAX_BODY", cfg.HTTP.MaxBody)
	if cfg.Stores.NATSReplicas < 1 {
		p = append(p, "EB_NATS_REPLICAS: must be >= 1")
	}
	checkDur("EB_AUTH_SESSION_TTL", cfg.Auth.SessionTTL)
	checkDur("EB_AUTH_SESSION_IDLE", cfg.Auth.SessionIdle)
	checkDur("EB_AUTH_LOCKOUT_DURATION", cfg.Auth.LockoutDuration)
	checkDur("EB_LOG_RETENTION", cfg.Log.Retention)
	checkSize("EB_LOG_DISK_CAP", cfg.Log.DiskCap)
	checkDur("EB_SYNC_RESYNC_DELAY", cfg.Sync.ResyncDelay)
	checkDur("EB_SYNC_MAX_IDLE", cfg.Sync.MaxIdle)
	checkDur("EB_LEASE_TTL", cfg.Lease.LeaseTTL)
	checkDur("EB_LEASE_RENEW", cfg.Lease.LeaseRenew)
	checkSize("EB_SUBMIT_MAX_MESSAGE_SIZE", cfg.Submit.MaxMessageSize)
	checkDur("EB_WEBHOOK_TIMEOUT", cfg.Webhook.Timeout)
	checkDur("EB_RULES_TIME_LIMIT", cfg.Webhook.RulesTimeLimit)
	for _, d := range []struct {
		k string
		v time.Duration
	}{
		{"EB_RETENTION_EVENTS", cfg.Retention.Events},
		{"EB_RETENTION_DELIVERIES", cfg.Retention.Deliveries},
		{"EB_RETENTION_OUTBOUND", cfg.Retention.Outbound},
		{"EB_RETENTION_OUTBOUND_RAW", cfg.Retention.OutboundRaw},
		{"EB_RETENTION_SANDBOX", cfg.Retention.Sandbox},
		{"EB_RETENTION_JOBS", cfg.Retention.Jobs},
		{"EB_RETENTION_AUDIT", cfg.Retention.Audit},
		{"EB_RETENTION_TRACKING", cfg.Retention.Tracking},
	} {
		checkDur(d.k, d.v)
	}
	return p
}

func unknownWarnings(env Env, known map[string]struct{}) []string {
	var warns []string
	for _, kv := range env.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		k := kv[:eq]
		if !strings.HasPrefix(k, "EB_") {
			continue
		}
		if strings.HasPrefix(k, "EB_TEST_") {
			continue
		}
		if _, ok := known[k]; ok {
			continue
		}
		warns = append(warns, fmt.Sprintf("unknown environment key %s", k))
	}
	return warns
}

// Validate checks role-dependent rules. runsAPI is true when this node runs the api role.
// The config package must not import internal/app.
func (c *Config) Validate(runsAPI bool) error {
	var problems []string
	if runsAPI && c.HTTP.PublicURL == "" {
		problems = append(problems, "EB_PUBLIC_URL: required when the node runs the api role")
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}
