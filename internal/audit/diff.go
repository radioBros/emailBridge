package audit

import (
	"encoding/json"
	"reflect"
	"strings"
)

const redacted = "[redacted]"

// Diff returns field-level before/after changes. Struct fields use their json tag names
// (falling back to the Go name). secretFields (and any Secret-typed values) are stored as
// "[redacted]". Maps are compared by key; nested structs/maps are flattened with dotted paths
// only one level deep for maps-of-structs — top-level keys for flat structs.
func Diff(before, after any, secretFields ...string) Changes {
	secrets := map[string]bool{}
	for _, s := range secretFields {
		secrets[normKey(s)] = true
	}
	bm := toMap(before)
	am := toMap(after)
	keys := map[string]struct{}{}
	for k := range bm {
		keys[k] = struct{}{}
	}
	for k := range am {
		keys[k] = struct{}{}
	}
	out := Changes{}
	for k := range keys {
		bv, bOK := bm[k]
		av, aOK := am[k]
		if bOK && aOK && reflect.DeepEqual(bv, av) {
			continue
		}
		if !bOK && (!aOK || isEmpty(av)) {
			continue
		}
		if !aOK && isEmpty(bv) {
			continue
		}
		fc := FieldChange{}
		if bOK {
			fc.Before = redactValue(k, bv, secrets)
		}
		if aOK {
			fc.After = redactValue(k, av, secrets)
		}
		out[k] = fc
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func redactValue(key string, v any, secrets map[string]bool) any {
	if _, ok := v.(Secret); ok {
		return redacted
	}
	if secrets[normKey(key)] || sensitiveKey(key) {
		return redacted
	}
	return v
}

func normKey(k string) string {
	k = strings.ToLower(k)
	k = strings.ReplaceAll(k, "_", "")
	return strings.ReplaceAll(k, "-", "")
}

func sensitiveKey(key string) bool {
	k := normKey(key)
	if i := strings.LastIndexByte(k, '.'); i >= 0 {
		k = k[i+1:]
	}
	exact := map[string]bool{"passwd": true, "cookie": true, "authorization": true, "dsn": true, "otp": true}
	if exact[k] {
		return true
	}
	for _, s := range []string{"pass", "password", "secret", "token", "apikey", "privatekey", "credentials"} {
		if strings.HasSuffix(k, s) {
			return true
		}
	}
	return false
}

func toMap(v any) map[string]any {
	if v == nil {
		return map[string]any{}
	}
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = unwrap(vv)
		}
		return out
	case Changes:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = vv
		}
		return out
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return map[string]any{}
	}
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return map[string]any{}
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			out[key.String()] = unwrap(rv.MapIndex(key).Interface())
		}
		return out
	case reflect.Struct:
		// Prefer JSON tags via round-trip so nested shapes match API names.
		b, err := json.Marshal(v)
		if err != nil {
			return structFields(rv)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil || m == nil {
			return structFields(rv)
		}
		return m
	default:
		return map[string]any{"value": unwrap(v)}
	}
}

func structFields(rv reflect.Value) map[string]any {
	t := rv.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if sf.PkgPath != "" { // unexported
			continue
		}
		name := sf.Name
		if tag := sf.Tag.Get("json"); tag != "" && tag != "-" {
			name, _, _ = strings.Cut(tag, ",")
			if name == "" || name == "-" {
				continue
			}
		}
		out[name] = unwrap(rv.Field(i).Interface())
	}
	return out
}

func unwrap(v any) any {
	if s, ok := v.(Secret); ok {
		return s
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil
	}
	for rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
		v = rv.Interface()
	}
	return v
}

func isEmpty(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return true
	}
	switch rv.Kind() {
	case reflect.String, reflect.Slice, reflect.Map, reflect.Array:
		return rv.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return rv.IsNil()
	}
	return false
}
