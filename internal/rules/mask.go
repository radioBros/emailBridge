package rules

import "strings"

//nolint:gosec // G101: display mask for secret env values, not a credential
const secretMask = "••••"

// MaskSecrets replaces any string value in mapped that equals a secret env value with "••••" (S8 §5).
func MaskSecrets(mapped any, secretValues []string) any {
	if mapped == nil || len(secretValues) == 0 {
		return mapped
	}
	set := make(map[string]struct{}, len(secretValues))
	for _, v := range secretValues {
		if v != "" {
			set[v] = struct{}{}
		}
	}
	if len(set) == 0 {
		return mapped
	}
	return maskWalk(mapped, set)
}

func maskWalk(v any, secrets map[string]struct{}) any {
	switch x := v.(type) {
	case string:
		if _, ok := secrets[x]; ok {
			return secretMask
		}
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = maskWalk(vv, secrets)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = maskWalk(vv, secrets)
		}
		return out
	default:
		return v
	}
}

// SecretValuesFromEnv returns values from env whose keys are listed in secretKeys (case-sensitive).
func SecretValuesFromEnv(env map[string]string, secretKeys []string) []string {
	if len(env) == 0 || len(secretKeys) == 0 {
		return nil
	}
	var out []string
	for _, k := range secretKeys {
		if v, ok := env[k]; ok && v != "" {
			out = append(out, v)
		}
	}
	return out
}

// MaskString replaces exact occurrences of secret values inside s (used for error excerpts).
func MaskString(s string, secretValues []string) string {
	for _, v := range secretValues {
		if v != "" {
			s = strings.ReplaceAll(s, v, secretMask)
		}
	}
	return s
}
