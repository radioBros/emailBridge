package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/config"
)

// KeyKind classifies a runtime setting for validation and display.
type KeyKind string

// Key kinds mirrored from config field types.
const (
	KindString   KeyKind = "string"
	KindInt      KeyKind = "int"
	KindBool     KeyKind = "bool"
	KindFloat    KeyKind = "float"
	KindDuration KeyKind = "duration"
	KindSize     KeyKind = "size"
	KindRate     KeyKind = "rate"
	KindStrings  KeyKind = "strings"
	KindAny      KeyKind = "any"
)

// KeyMeta describes one runtime-changeable setting.
type KeyMeta struct {
	Name        string // camelCase API/storage name (e.g. webhookTimeout)
	EnvKey      string // EB_* (empty for runtime-only)
	Kind        KeyKind
	Secret      bool
	Default     string
	OneOf       []string
	Scope       string // "global" | "organization" | "both"
	RuntimeOnly bool
}

// runtimeKeysFromConfig reflects config.Config for fields tagged runtime:"true".
func runtimeKeysFromConfig() []KeyMeta {
	var out []KeyMeta
	walkConfig(reflect.TypeOf(config.Config{}), &out)
	return out
}

func walkConfig(t reflect.Type, out *[]KeyMeta) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		ft := sf.Type
		if ft.Kind() == reflect.Struct && ft != reflect.TypeOf(time.Duration(0)) &&
			ft != reflect.TypeOf(config.Rate{}) && ft != reflect.TypeOf(config.Size(0)) {
			if sf.Tag.Get("env") == "" {
				walkConfig(ft, out)
				continue
			}
		}
		env := sf.Tag.Get("env")
		if env == "" || sf.Tag.Get("runtime") != "true" {
			continue
		}
		*out = append(*out, KeyMeta{
			Name:    envToCamel(env),
			EnvKey:  env,
			Kind:    kindOf(ft),
			Secret:  sf.Tag.Get("secret") == "true",
			Default: sf.Tag.Get("default"),
			OneOf:   splitOneOf(sf.Tag.Get("oneof")),
			Scope:   "both",
		})
	}
}

func kindOf(t reflect.Type) KeyKind {
	switch t {
	case reflect.TypeOf(""):
		return KindString
	case reflect.TypeOf(0):
		return KindInt
	case reflect.TypeOf(int64(0)):
		return KindInt
	case reflect.TypeOf(false):
		return KindBool
	case reflect.TypeOf(float64(0)):
		return KindFloat
	case reflect.TypeOf(time.Duration(0)):
		return KindDuration
	case reflect.TypeOf(config.Size(0)):
		return KindSize
	case reflect.TypeOf(config.Rate{}):
		return KindRate
	case reflect.TypeOf([]string(nil)):
		return KindStrings
	default:
		return KindAny
	}
}

func splitOneOf(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "|")
}

// envToCamel turns EB_WEBHOOK_TIMEOUT into webhookTimeout.
func envToCamel(env string) string {
	s := strings.TrimPrefix(env, "EB_")
	parts := strings.Split(strings.ToLower(s), "_")
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(parts[0])
	for _, p := range parts[1:] {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		if len(p) > 1 {
			b.WriteString(p[1:])
		}
	}
	return b.String()
}

// runtimeOnlyKeys are S9 §2.14 settings with no env key.
func runtimeOnlyKeys() []KeyMeta {
	return []KeyMeta{
		{Name: "sandbox.rewriteTo", Kind: KindString, Scope: "both", RuntimeOnly: true},
		{Name: "authResults.trustedAuthservIds", Kind: KindStrings, Scope: "organization", Default: "[]", RuntimeOnly: true},
		{Name: "authResults.mode", Kind: KindString, Scope: "organization", Default: "header", RuntimeOnly: true},
		{Name: "protocolLogs.accountCap", Kind: KindSize, Scope: "both", Default: "10MiB", RuntimeOnly: true},
		{Name: "protocolLogs.rawDebugTtl", Kind: KindDuration, Scope: "global", Default: "1h", RuntimeOnly: true},
		{Name: "oidc.autoProvision", Kind: KindBool, Scope: "organization", Default: "false", RuntimeOnly: true},
		{Name: "oidc.groupRoles", Kind: KindAny, Scope: "organization", Default: "{}", RuntimeOnly: true},
		{Name: "webhooks.autoDisableAfter", Kind: KindDuration, Scope: "organization", Default: "5d", RuntimeOnly: true},
		// Applied when an endpoint is created or updated without its own retry block; existing endpoints keep their values (S7 §3).
		{Name: "webhook.retryDefaults", Kind: KindAny, Scope: "both", Default: `{"maxAttempts":10,"backoffBase":"5s","backoffJitter":0.2,"timeout":"30s"}`, RuntimeOnly: true},
		// Instance owners only: allow SQS/SNS/Pub/Sub sinks to use the server's cloud identity (S7 §6).
		{Name: "sinks.ambientCredentials", Kind: KindBool, Scope: "global", Default: "false", RuntimeOnly: true},
		// P4.3 attachments
		{Name: "attachments.signedUrlTtl", Kind: KindDuration, Scope: "both", Default: "15m", RuntimeOnly: true},
		{Name: "attachments.scanMaxSize", Kind: KindSize, Scope: "both", Default: "25MiB", RuntimeOnly: true},
		{Name: "attachments.scanTimeout", Kind: KindDuration, Scope: "both", Default: "30s", RuntimeOnly: true},
		// P7.3 MCP PII controls (org)
		{Name: "mcp.piiRedact", Kind: KindBool, Scope: "organization", Default: "true", RuntimeOnly: true},
		{Name: "mcp.bodyMaxChars", Kind: KindInt, Scope: "organization", Default: "4000", RuntimeOnly: true},
		// MCP server dedicated listener (docs/spec/MCP-server.md): "" = off, "8090" / ":8090" / "127.0.0.1:8090".
		{Name: "mcp.listen", Kind: KindString, Scope: "global", Default: "", RuntimeOnly: true},
		// P5.5 throttle / send windows / pre-send (docs/spec/P5.5-throttle.md)
		{Name: "sending.throttle.limits", Kind: KindAny, Scope: "both", Default: `{"domainPerMinute":60,"providers":{"gmail":20,"outlook":30,"yahoo":20,"icloud":20,"libero":30,"aruba":30},"retryAfter":"30s"}`, RuntimeOnly: true},
		{Name: "sending.sendWindows.config", Kind: KindAny, Scope: "organization", Default: "{}", RuntimeOnly: true},
		{Name: "sending.preSendHook.cel", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		// P8.1 Rspamd settings preset
		{Name: "integrations.rspamd.baseUrl", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.rspamd.timeout", Kind: KindDuration, Scope: "organization", Default: "5s", RuntimeOnly: true},
		// P8.3 sending / storage / CRM / SSO / observability presets
		{Name: "integrations.ses.region", Kind: KindString, Scope: "organization", Default: "eu-west-1", RuntimeOnly: true},
		{Name: "integrations.ses.from", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.postmark.from", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.mailgun.domain", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.mailgun.from", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.sendgrid.from", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.brevo.from", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.gdrive.folderId", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.onedrive.driveId", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.onedrive.folderPath", Kind: KindString, Scope: "organization", Default: "/EmailBridge", RuntimeOnly: true},
		{Name: "integrations.hubspot.portalId", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.pipedrive.companyDomain", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "integrations.sentry.sampleRate", Kind: KindString, Scope: "organization", Default: "1.0", RuntimeOnly: true},
		{Name: "integrations.sentry.environment", Kind: KindString, Scope: "organization", Default: "production", RuntimeOnly: true},
		{Name: "integrations.grafana.dashboardPath", Kind: KindString, Scope: "organization", Default: "deploy/observability/presets/grafana/emailbridge.json", RuntimeOnly: true},
		{Name: "integrations.loki.serviceLabel", Kind: KindString, Scope: "organization", Default: "emailbridge", RuntimeOnly: true},
		{Name: "integrations.otel.collectorPath", Kind: KindString, Scope: "organization", Default: "deploy/observability/presets/otel/collector.yaml", RuntimeOnly: true},
		// P9.2 evidence archive (S17 §9)
		{Name: "pec.evidenceRetention", Kind: KindDuration, Scope: "organization", Default: "3650d", RuntimeOnly: true},
		{Name: "pec.tsaUrl", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		// W54 external credential server (org)
		{Name: "authServer.url", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true},
		{Name: "authServer.hmacSecret", Kind: KindString, Scope: "organization", Default: "", RuntimeOnly: true, Secret: true},
		{Name: "authServer.ttl", Kind: KindDuration, Scope: "organization", Default: "5m", RuntimeOnly: true},
		{Name: "authServer.timeout", Kind: KindDuration, Scope: "organization", Default: "5s", RuntimeOnly: true},
		// W57 outbound IP pools (G25)
		{Name: "ipPool.localAddresses", Kind: KindStrings, Scope: "both", Default: "[]", RuntimeOnly: true},
		{Name: "ipPool.smtpStrategy", Kind: KindString, Scope: "both", Default: "round-robin", OneOf: []string{"random", "round-robin", "hash-by-account"}, RuntimeOnly: true},
		{Name: "ipPool.imapStrategy", Kind: KindString, Scope: "both", Default: "round-robin", OneOf: []string{"random", "round-robin", "hash-by-account"}, RuntimeOnly: true},
		// W75 system notifications
		{Name: "notifications.watchers.interval", Kind: KindDuration, Scope: "global", Default: "1m", RuntimeOnly: true},
		{Name: "notifications.webhooks.failureThreshold", Kind: KindInt, Scope: "both", Default: "5", RuntimeOnly: true},
		{Name: "notifications.webhooks.deadLetterGrowth", Kind: KindInt, Scope: "both", Default: "10", RuntimeOnly: true},
		{Name: "notifications.queues.pendingMinutes", Kind: KindDuration, Scope: "global", Default: "15m", RuntimeOnly: true},
		{Name: "notifications.queues.oldestAge", Kind: KindDuration, Scope: "global", Default: "30m", RuntimeOnly: true},
		{Name: "notifications.queues.pausedLongerThan", Kind: KindDuration, Scope: "global", Default: "1h", RuntimeOnly: true},
		{Name: "notifications.accounts.syncStall", Kind: KindDuration, Scope: "both", Default: "2h", RuntimeOnly: true},
		{Name: "notifications.accounts.certExpiryDays", Kind: KindInt, Scope: "both", Default: "14", RuntimeOnly: true},
		{Name: "notifications.sending.failureRatePct", Kind: KindInt, Scope: "both", Default: "20", RuntimeOnly: true},
		{Name: "notifications.platform.nodeMissing", Kind: KindDuration, Scope: "global", Default: "2m", RuntimeOnly: true},
		{Name: "notifications.security.apiKeyExpiryDays", Kind: KindInt, Scope: "both", Default: "7", RuntimeOnly: true},
		{Name: "notifications.security.quotaWarnPct", Kind: KindInt, Scope: "both", Default: "90", RuntimeOnly: true},
		{Name: "notifications.email.throttle", Kind: KindDuration, Scope: "both", Default: "6h", RuntimeOnly: true},
		{Name: "notifications.retention.resolvedDays", Kind: KindInt, Scope: "global", Default: "90", RuntimeOnly: true},
		// W76 org defaults for users who have not saved their own preferences
		{Name: "notifications.defaults.minSeverity", Kind: KindString, Scope: "organization", Default: "warning",
			OneOf: []string{"info", "warning", "critical"}, RuntimeOnly: true},
		{Name: "notifications.defaults.digest", Kind: KindString, Scope: "organization", Default: "hourly",
			OneOf: []string{"hourly", "daily", "immediate"}, RuntimeOnly: true},
		{Name: "notifications.defaults.emailEnabled", Kind: KindBool, Scope: "organization", Default: "true", RuntimeOnly: true},
	}
}

// Catalog is every runtime key (env-backed + §2.14) keyed by camelCase / dotted name.
func Catalog() map[string]KeyMeta {
	out := map[string]KeyMeta{}
	for _, k := range runtimeKeysFromConfig() {
		out[k.Name] = k
	}
	for _, k := range runtimeOnlyKeys() {
		out[k.Name] = k
	}
	return out
}

// ParseValue validates and canonicalises a patch value for key m.
func ParseValue(m KeyMeta, raw any) (any, error) {
	if raw == nil {
		return nil, nil
	}
	switch m.Kind {
	case KindBool:
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			return config.ParseBool(v)
		default:
			return nil, fmt.Errorf("want bool")
		}
	case KindInt:
		switch v := raw.(type) {
		case float64:
			return int(v), nil
		case int:
			return v, nil
		case int64:
			return int(v), nil
		case string:
			var i int
			_, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &i)
			if err != nil {
				return nil, fmt.Errorf("want integer")
			}
			return i, nil
		default:
			return nil, fmt.Errorf("want integer")
		}
	case KindFloat:
		switch v := raw.(type) {
		case float64:
			return v, nil
		case int:
			return float64(v), nil
		case string:
			var f float64
			_, err := fmt.Sscanf(strings.TrimSpace(v), "%f", &f)
			return f, err
		default:
			return nil, fmt.Errorf("want float")
		}
	case KindDuration:
		s, err := asString(raw)
		if err != nil {
			return nil, err
		}
		d, err := config.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		return formatDuration(d), nil
	case KindSize:
		s, err := asString(raw)
		if err != nil {
			if n, ok := asInt(raw); ok {
				return config.Size(n).String(), nil
			}
			return nil, err
		}
		sz, err := config.ParseSize(s)
		if err != nil {
			return nil, err
		}
		return sz.String(), nil
	case KindRate:
		s, err := asString(raw)
		if err != nil {
			return nil, err
		}
		r, err := config.ParseRate(s)
		if err != nil {
			return nil, err
		}
		return r.String(), nil
	case KindStrings:
		switch v := raw.(type) {
		case []any:
			out := make([]string, 0, len(v))
			for _, x := range v {
				s, ok := x.(string)
				if !ok {
					return nil, fmt.Errorf("want string array")
				}
				out = append(out, s)
			}
			return out, nil
		case []string:
			return v, nil
		case string:
			return config.ParseStringList(v), nil
		default:
			return nil, fmt.Errorf("want string array")
		}
	case KindString:
		s, err := asString(raw)
		if err != nil {
			return nil, err
		}
		if len(m.OneOf) > 0 {
			ok := false
			for _, o := range m.OneOf {
				if s == o {
					ok = true
					break
				}
			}
			if !ok {
				return nil, fmt.Errorf("want one of %s", strings.Join(m.OneOf, "|"))
			}
		}
		return s, nil
	default:
		if m.Name == "webhook.retryDefaults" {
			return parseRetryDefaults(raw)
		}
		return raw, nil
	}
}

// parseRetryDefaults validates webhook.retryDefaults like an endpoint retry block.
func parseRetryDefaults(raw any) (any, error) {
	obj, ok := asObject(raw)
	if !ok {
		if s, err := asString(raw); err == nil {
			var decoded map[string]any
			if jsonErr := json.Unmarshal([]byte(s), &decoded); jsonErr != nil {
				return nil, fmt.Errorf("want retry object")
			}
			obj = decoded
		} else {
			return nil, fmt.Errorf("want retry object")
		}
	}
	maxAttempts, ok := asInt(obj["maxAttempts"])
	if !ok || maxAttempts < 1 || maxAttempts > 100 {
		return nil, fmt.Errorf("maxAttempts must be 1..100")
	}
	backoffBase, err := asString(obj["backoffBase"])
	if err != nil || backoffBase == "" {
		return nil, fmt.Errorf("backoffBase required")
	}
	if _, err := config.ParseDuration(backoffBase); err != nil {
		return nil, fmt.Errorf("backoffBase: %w", err)
	}
	jitter, ok := obj["backoffJitter"].(float64)
	if !ok {
		if i, ok := asInt(obj["backoffJitter"]); ok {
			jitter = float64(i)
		} else {
			return nil, fmt.Errorf("backoffJitter required")
		}
	}
	if jitter < 0 || jitter > 1 {
		return nil, fmt.Errorf("backoffJitter must be 0..1")
	}
	timeout, err := asString(obj["timeout"])
	if err != nil || timeout == "" {
		return nil, fmt.Errorf("timeout required")
	}
	if _, err := config.ParseDuration(timeout); err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}
	return map[string]any{
		"maxAttempts":   int(maxAttempts),
		"backoffBase":   backoffBase,
		"backoffJitter": jitter,
		"timeout":       timeout,
	}, nil
}

func asString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	default:
		return "", fmt.Errorf("want string")
	}
}

func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case float32:
		return int64(x), true
	case int:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	}
	return 0, false
}

func formatDuration(d time.Duration) string {
	if d%(24*time.Hour) == 0 && d >= 24*time.Hour {
		return fmt.Sprintf("%dd", int64(d/(24*time.Hour)))
	}
	return d.String()
}

// DisplayBootValue renders a boot Config field as a JSON-ish value for GET /v1/settings.
func DisplayBootValue(m KeyMeta, cfg *config.Config) any {
	if cfg == nil || m.EnvKey == "" {
		if m.Default != "" {
			v, err := ParseValue(m, m.Default)
			if err == nil {
				return v
			}
			return m.Default
		}
		return nil
	}
	v := findEnvValue(reflect.ValueOf(cfg).Elem(), m.EnvKey)
	if !v.IsValid() {
		return nil
	}
	return displayReflect(v, m)
}

func findEnvValue(rv reflect.Value, envKey string) reflect.Value {
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		fv := rv.Field(i)
		if sf.Type.Kind() == reflect.Struct && sf.Tag.Get("env") == "" &&
			sf.Type != reflect.TypeOf(time.Duration(0)) &&
			sf.Type != reflect.TypeOf(config.Rate{}) && sf.Type != reflect.TypeOf(config.Size(0)) {
			if found := findEnvValue(fv, envKey); found.IsValid() {
				return found
			}
			continue
		}
		if sf.Tag.Get("env") == envKey {
			return fv
		}
	}
	return reflect.Value{}
}

func displayReflect(v reflect.Value, m KeyMeta) any {
	if m.Secret {
		s := fmt.Sprint(v.Interface())
		return maskSecret(s)
	}
	switch m.Kind {
	case KindDuration:
		return formatDuration(v.Interface().(time.Duration))
	case KindSize:
		return v.Interface().(config.Size).String()
	case KindRate:
		return v.Interface().(config.Rate).String()
	case KindStrings:
		return v.Interface().([]string)
	default:
		return v.Interface()
	}
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	last := s
	if len(s) > 4 {
		last = s[len(s)-4:]
	}
	return "••••" + last
}
