package integrations

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/radiobros/emailbridge/internal/errs"
)

// validateConfig checks config (+ optional secrets) against a JSON Schema object (subset).
// Supported: type object, properties (string/number/integer/boolean), required, format uri,
// writeOnly (ignored here — secrets are validated separately), minLength, maxLength, minimum, maximum.
func validateConfig(schema map[string]any, config map[string]any, secrets map[string]string, secretFields []string) error {
	if schema == nil {
		return nil
	}
	if config == nil {
		config = map[string]any{}
	}
	if secrets == nil {
		secrets = map[string]string{}
	}
	props, _ := schema["properties"].(map[string]any)
	required, _ := schema["required"].([]any)
	secretSet := map[string]bool{}
	for _, f := range secretFields {
		secretSet[f] = true
	}
	for _, r := range required {
		name, _ := r.(string)
		if name == "" {
			continue
		}
		if secretSet[name] {
			if strings.TrimSpace(secrets[name]) == "" {
				return errs.New(errs.CodeValidationFailed, "Missing required secret.").WithSafe("field", name)
			}
			continue
		}
		if _, ok := config[name]; !ok {
			return errs.New(errs.CodeValidationFailed, "Missing required field.").WithSafe("field", name)
		}
	}
	if props == nil {
		return nil
	}
	for name, raw := range props {
		ps, _ := raw.(map[string]any)
		if ps == nil {
			continue
		}
		writeOnly, _ := ps["writeOnly"].(bool)
		if writeOnly || secretSet[name] {
			if v, ok := secrets[name]; ok && v != "" {
				if err := checkStringConstraints(name, v, ps); err != nil {
					return err
				}
			}
			continue
		}
		v, ok := config[name]
		if !ok {
			continue
		}
		if err := checkValue(name, v, ps); err != nil {
			return err
		}
	}
	// Reject unknown config keys that are not secrets.
	for k := range config {
		if props != nil {
			if _, ok := props[k]; !ok {
				return errs.New(errs.CodeValidationFailed, "Unknown config field.").WithSafe("field", k)
			}
		}
		if secretSet[k] {
			return errs.New(errs.CodeValidationFailed, "Secret fields must be sent in secrets.").WithSafe("field", k)
		}
	}
	return nil
}

func checkValue(name string, v any, ps map[string]any) error {
	typ, _ := ps["type"].(string)
	switch typ {
	case "string":
		s, ok := v.(string)
		if !ok {
			return errs.New(errs.CodeValidationFailed, "Field must be a string.").WithSafe("field", name)
		}
		return checkStringConstraints(name, s, ps)
	case "boolean":
		if _, ok := v.(bool); !ok {
			return errs.New(errs.CodeValidationFailed, "Field must be a boolean.").WithSafe("field", name)
		}
	case "integer", "number":
		switch v.(type) {
		case float64, int, int32, int64:
		default:
			return errs.New(errs.CodeValidationFailed, "Field must be a number.").WithSafe("field", name)
		}
		n := asFloat(v)
		if min, ok := asFloatOK(ps["minimum"]); ok && n < min {
			return errs.New(errs.CodeValidationFailed, "Field below minimum.").WithSafe("field", name)
		}
		if max, ok := asFloatOK(ps["maximum"]); ok && n > max {
			return errs.New(errs.CodeValidationFailed, "Field above maximum.").WithSafe("field", name)
		}
	case "", "object":
		// nested objects accepted as-is in P8.1
	default:
		return errs.New(errs.CodeValidationFailed, fmt.Sprintf("Unsupported schema type %q.", typ)).WithSafe("field", name)
	}
	return nil
}

func checkStringConstraints(name, s string, ps map[string]any) error {
	if min, ok := asIntOK(ps["minLength"]); ok && len(s) < min {
		return errs.New(errs.CodeValidationFailed, "Field too short.").WithSafe("field", name)
	}
	if max, ok := asIntOK(ps["maxLength"]); ok && len(s) > max {
		return errs.New(errs.CodeValidationFailed, "Field too long.").WithSafe("field", name)
	}
	if fmtStr, _ := ps["format"].(string); fmtStr == "uri" {
		u, err := url.ParseRequestURI(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return errs.New(errs.CodeValidationFailed, "Field must be an http(s) URI.").WithSafe("field", name)
		}
	}
	return nil
}

func asFloat(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int32:
		return float64(n)
	case int64:
		return float64(n)
	default:
		return 0
	}
}

func asFloatOK(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	return asFloat(v), true
}

func asIntOK(v any) (int, bool) {
	if v == nil {
		return 0, false
	}
	return int(asFloat(v)), true
}

// splitConfigSecrets moves writeOnly / secretFields from a combined map into secrets.
func splitConfigSecrets(def *Definition, config map[string]any, secrets map[string]string) (map[string]any, map[string]string) {
	outCfg := map[string]any{}
	outSec := map[string]string{}
	for k, v := range secrets {
		outSec[k] = v
	}
	secretSet := map[string]bool{}
	for _, f := range def.SecretFields {
		secretSet[f] = true
	}
	props, _ := def.ConfigSchema["properties"].(map[string]any)
	for k, v := range config {
		writeOnly := false
		if props != nil {
			if ps, ok := props[k].(map[string]any); ok {
				writeOnly, _ = ps["writeOnly"].(bool)
			}
		}
		if writeOnly || secretSet[k] {
			if s, ok := v.(string); ok {
				outSec[k] = s
			}
			continue
		}
		outCfg[k] = v
	}
	return outCfg, outSec
}
