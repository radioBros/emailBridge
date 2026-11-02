package throttle

import (
	"encoding/json"
	"time"
)

// Limits is the parsed sending.throttle.limits setting.
type Limits struct {
	DomainPerMinute int            `json:"domainPerMinute"`
	Providers       map[string]int `json:"providers"`
	Domains         map[string]int `json:"domains"`
	RetryAfter      string         `json:"retryAfter"`
}

// DefaultLimits returns the P5.5 presets.
func DefaultLimits() Limits {
	return Limits{
		DomainPerMinute: 60,
		Providers: map[string]int{
			"gmail": 20, "outlook": 30, "yahoo": 20, "icloud": 20, "libero": 30, "aruba": 30,
		},
		RetryAfter: "30s",
	}
}

// ParseLimits decodes JSON; nil/empty → defaults. Unknown fields ignored.
func ParseLimits(raw any) Limits {
	d := DefaultLimits()
	if raw == nil {
		return d
	}
	var b []byte
	switch v := raw.(type) {
	case string:
		if v == "" || v == "{}" {
			return d
		}
		b = []byte(v)
	case []byte:
		b = v
	case map[string]any:
		b, _ = json.Marshal(v)
	default:
		b, _ = json.Marshal(v)
	}
	if len(b) == 0 {
		return d
	}
	_ = json.Unmarshal(b, &d)
	if d.DomainPerMinute <= 0 {
		d.DomainPerMinute = 60
	}
	if d.Providers == nil {
		d.Providers = DefaultLimits().Providers
	}
	return d
}

// RetryDelay returns the configured retry delay (default 30s).
func (l Limits) RetryDelay() time.Duration {
	if l.RetryAfter == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(l.RetryAfter)
	if err != nil || d <= 0 {
		return 30 * time.Second
	}
	return d
}

// DomainLimit returns the per-minute cap for a destination domain.
func (l Limits) DomainLimit(domain string) int {
	if l.Domains != nil {
		if n, ok := l.Domains[domain]; ok && n > 0 {
			return n
		}
	}
	return l.DomainPerMinute
}

// ProviderLimit returns the per-minute cap for a family, or 0 when unset (skip provider bucket).
func (l Limits) ProviderLimit(family string) int {
	if family == "" || l.Providers == nil {
		return 0
	}
	return l.Providers[family]
}
