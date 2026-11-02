package config

import (
	"fmt"
	"net/netip"
	"reflect"
	"time"
)

// Redacted returns every config key mapped to its value, with secret keys replaced
// by "[redacted]". Used to log the loaded configuration at startup.
func (c *Config) Redacted() map[string]any {
	out := make(map[string]any)
	metas := collectMeta(c)
	for _, m := range metas {
		if m.Secret {
			out[m.Key] = "[redacted]"
			continue
		}
		out[m.Key] = displayValue(m)
	}
	return out
}

func displayValue(m fieldMeta) any {
	switch m.Kind {
	case kindString:
		return *(m.Field.(*string))
	case kindInt:
		return *(m.Field.(*int))
	case kindInt64:
		return *(m.Field.(*int64))
	case kindBool:
		return *(m.Field.(*bool))
	case kindFloat64:
		return *(m.Field.(*float64))
	case kindDuration:
		return (*(m.Field.(*time.Duration))).String()
	case kindSize:
		return (*(m.Field.(*Size))).String()
	case kindStringSlice:
		return *(m.Field.(*[]string))
	case kindPrefixSlice:
		ps := *(m.Field.(*[]netip.Prefix))
		ss := make([]string, len(ps))
		for i, p := range ps {
			ss[i] = p.String()
		}
		return ss
	case kindRate:
		return (*(m.Field.(*Rate))).String()
	case kindKeyRing:
		return (*(m.Field.(*KeyRing))).Kids()
	default:
		v := reflect.ValueOf(m.Field).Elem()
		return fmt.Sprintf("%v", v.Interface())
	}
}
