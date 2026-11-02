package logging

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// Secret is a string that is always logged as "[redacted]": slog.Any("dsn", logging.Secret(dsn)).
type Secret string

// LogValue implements slog.LogValuer.
func (Secret) LogValue() slog.Value { return slog.StringValue(redacted) }

// String never reveals the value, even if formatted directly.
func (Secret) String() string { return redacted }

const redacted = "[redacted]"

type address string

// Address returns an attribute holding an email address. It is logged as is, or as
// "h:<12 hex of HMAC-SHA256>" when address hashing is on (S12 §11).
func Address(key, addr string) slog.Attr { return slog.Any(key, address(addr)) }

// Size returns an attribute that records only the length of a payload: "{N bytes}".
func Size(key string, n int) slog.Attr { return slog.String(key, fmt.Sprintf("{%d bytes}", n)) }

// exact and suffix lists of attribute keys whose values are always redacted. Keys are normalised to
// lowercase without '_' and '-' before matching.
var (
	redactExact  = map[string]bool{"passwd": true, "cookie": true, "setcookie": true, "authorization": true, "dsn": true, "otp": true}
	redactSuffix = []string{"pass", "password", "secret", "token", "apikey", "privatekey", "credentials"}
	urlSecrets   = map[string]bool{"token": true, "sig": true, "signature": true, "key": true, "apikey": true, "accesstoken": true, "code": true, "password": true}
)

func normKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	return strings.ReplaceAll(k, "-", "")
}

// SensitiveKey reports whether a field or attribute named key holds a secret (password, token, API key …),
// with the same rules as log redaction. Other packages use it to keep secrets out of stored data.
func SensitiveKey(key string) bool { return sensitiveKey(key, "") }

// Redacted is the marker that replaces a secret.
const Redacted = redacted

// sensitiveKey reports whether values under key must be redacted. In the auth category the key "code"
// (OTP and verification codes) is sensitive too.
func sensitiveKey(key string, cat Category) bool {
	k := normKey(key)
	if i := strings.LastIndexByte(k, '.'); i >= 0 {
		k = k[i+1:]
	}
	if redactExact[k] {
		return true
	}
	if cat == CatAuth && k == "code" {
		return true
	}
	for _, s := range redactSuffix {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

// redactor turns slog values into plain, redacted JSON-able values.
type redactor struct {
	cat     Category
	hash    bool
	hashKey []byte
}

func (r redactor) attr(key string, v slog.Value) any {
	if sensitiveKey(key, r.cat) {
		return redacted
	}
	return r.value(v)
}

func (r redactor) value(v slog.Value) any {
	v = v.Resolve()
	switch v.Kind() {
	case slog.KindGroup:
		m := make(map[string]any, len(v.Group()))
		for _, a := range v.Group() {
			m[a.Key] = r.attr(a.Key, a.Value)
		}
		return m
	case slog.KindString:
		return stripURL(v.String())
	case slog.KindInt64:
		return v.Int64()
	case slog.KindUint64:
		return v.Uint64()
	case slog.KindFloat64:
		return v.Float64()
	case slog.KindBool:
		return v.Bool()
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().UTC().Format("2006-01-02T15:04:05.000Z07:00")
	}
	switch x := v.Any().(type) {
	case address:
		return r.address(string(x))
	case []byte:
		if r.cat == CatProtocol && len(x) <= 64*1024 {
			return string(x)
		}
		return fmt.Sprintf("{%d bytes}", len(x))
	case error:
		return stripURL(x.Error())
	case fmt.Stringer:
		return stripURL(x.String())
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, vv := range x {
			m[k] = r.attr(k, slog.AnyValue(vv))
		}
		return m
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = stripURL(s)
		}
		return out
	default:
		return x
	}
}

func (r redactor) address(a string) string {
	if !r.hash || a == "" {
		return a
	}
	m := hmac.New(sha256.New, r.hashKey)
	m.Write([]byte(strings.ToLower(strings.TrimSpace(a))))
	return "h:" + hex.EncodeToString(m.Sum(nil))[:12]
}

// stripURL removes credentials and secret query parameters from strings that are URLs
// (mongodb://user:pass@host, https://x/cb?token=…). Other strings are returned unchanged.
func stripURL(s string) string {
	if !strings.Contains(s, "://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme == "" {
		return s
	}
	changed := false
	if u.User != nil {
		if _, has := u.User.Password(); has {
			u.User = url.UserPassword(u.User.Username(), "redacted")
			changed = true
		}
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if urlSecrets[normKey(k)] {
				q.Set(k, "redacted")
				changed = true
			}
		}
		if changed {
			u.RawQuery = q.Encode()
		}
	}
	if !changed {
		return s
	}
	return u.String()
}
