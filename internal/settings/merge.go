package settings

import (
	"encoding/json"
	"fmt"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/i18n"
)

const maxUserSettingsBytes = 64 * 1024

var pollutionKeys = map[string]bool{
	"__proto__": true, "constructor": true, "prototype": true,
}

// isMongoUnsafeKey reports field names MongoDB rejects or that enable operator injection.
// Dotted names are unsafe unless the caller allow-lists a known catalog/switch key.
func isMongoUnsafeKey(k string) bool {
	if k == "" || strings.ContainsRune(k, 0) {
		return true
	}
	if strings.HasPrefix(k, "$") {
		return true
	}
	return strings.Contains(k, ".")
}

func joinKeyPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// rejectClientKeys walks a client-supplied object and rejects pollution keys and Mongo-unsafe
// field names. allowDotted, when non-nil, may permit a dotted key (known setting/switch names).
func rejectClientKeys(m map[string]any, prefix string, allowDotted func(key string) bool) error {
	for k, v := range m {
		path := joinKeyPath(prefix, k)
		if pollutionKeys[k] || strings.HasPrefix(k, "__") {
			return errs.New(errs.CodeValidationFailed, "Prototype-pollution keys are not allowed.").WithSafe("key", path)
		}
		if k == "" || strings.ContainsRune(k, 0) || strings.HasPrefix(k, "$") {
			return errs.New(errs.CodeValidationFailed, "Invalid settings key.").WithSafe("key", path)
		}
		if strings.Contains(k, ".") && (allowDotted == nil || !allowDotted(k)) {
			return errs.New(errs.CodeValidationFailed, "Invalid settings key.").WithSafe("key", path)
		}
		if cm, ok := asObject(v); ok {
			if err := rejectClientKeys(cm, path, allowDotted); err != nil {
				return err
			}
		}
	}
	return nil
}

// rejectPollution rejects unsafe keys with no dotted-name allow-list (user settings, env).
func rejectPollution(m map[string]any, prefix string) error {
	return rejectClientKeys(m, prefix, nil)
}

// DeepMerge applies a JSON Merge Patch-style merge onto base:
// objects merge recursively; arrays, scalars and null replace; null deletes the key.
func DeepMerge(base map[string]any, patch map[string]any) (map[string]any, error) {
	if base == nil {
		base = map[string]any{}
	}
	out := cloneMap(base)
	for k, v := range patch {
		path := k
		if pollutionKeys[k] || strings.HasPrefix(k, "__") {
			return nil, errs.New(errs.CodeValidationFailed, "Prototype-pollution keys are not allowed.").WithSafe("key", path)
		}
		if isMongoUnsafeKey(k) {
			return nil, errs.New(errs.CodeValidationFailed, "Invalid settings key.").WithSafe("key", path)
		}
		if v == nil {
			delete(out, k)
			continue
		}
		if pm, ok := asObject(v); ok {
			existing, _ := asObject(out[k])
			merged, err := DeepMerge(existing, pm)
			if err != nil {
				return nil, err
			}
			out[k] = merged
			continue
		}
		out[k] = v
	}
	return out, nil
}

func asObject(v any) (map[string]any, bool) {
	switch x := v.(type) {
	case map[string]any:
		return x, true
	case bson.M:
		return map[string]any(x), true
	case bson.D:
		return bsonDToMap(x), true
	default:
		return nil, false
	}
}

func bsonDToMap(d bson.D) map[string]any {
	out := make(map[string]any, len(d))
	for _, e := range d {
		out[e.Key] = normalizeValue(e.Value)
	}
	return out
}

// normalizeValue converts BSON decode shapes (bson.D/M/A) into plain Go maps/slices.
func normalizeValue(v any) any {
	switch x := v.(type) {
	case bson.D:
		return bsonDToMap(x)
	case bson.M:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalizeValue(vv)
		}
		return out
	case bson.A:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeValue(vv)
		}
		return out
	default:
		return v
	}
}

func normalizeMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = normalizeValue(v)
	}
	return out
}

func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if cm, ok := asObject(v); ok {
			out[k] = cloneMap(cm)
			continue
		}
		out[k] = v
	}
	return out
}

// ValidateUserSettings checks tables.* and ui.* shapes (S2 §4.1) and size ≤ 64 KiB.
func ValidateUserSettings(s map[string]any) error {
	if s == nil {
		return nil
	}
	if err := rejectPollution(s, ""); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return errs.New(errs.CodeValidationFailed, "Settings are not valid JSON.")
	}
	if len(b) > maxUserSettingsBytes {
		return errs.New(errs.CodePayloadTooLarge, "User settings exceed 64 KiB.")
	}
	if tables, ok := asObject(s["tables"]); ok {
		for name, tv := range tables {
			tm, ok := asObject(tv)
			if !ok {
				return fieldErr("tables."+name, "must be an object")
			}
			if err := validateStringArray(tm, "visibility", "tables."+name+".visibility"); err != nil {
				return err
			}
			if err := validateStringArray(tm, "order", "tables."+name+".order"); err != nil {
				return err
			}
			if w, has := tm["widths"]; has && w != nil {
				wm, ok := asObject(w)
				if !ok {
					return fieldErr("tables."+name+".widths", "must be an object of numbers")
				}
				for wk, wv := range wm {
					if isMongoUnsafeKey(wk) || pollutionKeys[wk] {
						return errs.New(errs.CodeValidationFailed, "Invalid settings key.").WithSafe("key", "tables."+name+".widths."+wk)
					}
					switch wv.(type) {
					case float64, int, int64, json.Number:
					default:
						return fieldErr("tables."+name+".widths."+wk, "must be a number")
					}
				}
			}
		}
	}
	if ui, ok := asObject(s["ui"]); ok {
		for _, k := range []string{"theme", "density", "locale"} {
			if v, has := ui[k]; has && v != nil {
				sVal, ok := v.(string)
				if !ok {
					return fieldErr("ui."+k, "must be a string")
				}
				if k == "locale" && !i18n.IsSupported(sVal) {
					return fieldErr("ui.locale", "must be one of: "+strings.Join(i18n.Supported, ", "))
				}
			}
		}
	}
	if err := validateNotificationsSettings(s); err != nil {
		return err
	}
	return nil
}

// validateNotificationsSettings checks /v1/me/settings notifications (W75).
func validateNotificationsSettings(s map[string]any) error {
	n, ok := asObject(s["notifications"])
	if !ok || n == nil {
		return nil
	}
	if err := validateStringArray(n, "mutedCategories", "notifications.mutedCategories"); err != nil {
		return err
	}
	if email, ok := asObject(n["email"]); ok {
		if v, has := email["enabled"]; has && v != nil {
			if _, ok := v.(bool); !ok {
				return fieldErr("notifications.email.enabled", "must be a boolean")
			}
		}
		if v, has := email["digest"]; has && v != nil {
			ds, ok := v.(string)
			if !ok || (ds != "hourly" && ds != "daily" && ds != "immediate") {
				return fieldErr("notifications.email.digest", "must be hourly, daily or immediate")
			}
		}
		if v, has := email["minSeverity"]; has && v != nil {
			ms, ok := v.(string)
			if !ok || (ms != "info" && ms != "warning" && ms != "critical") {
				return fieldErr("notifications.email.minSeverity", "must be info, warning or critical")
			}
		}
		if cats, ok := asObject(email["categories"]); ok {
			for ck, cv := range cats {
				if isMongoUnsafeKey(ck) || pollutionKeys[ck] {
					return errs.New(errs.CodeValidationFailed, "Invalid settings key.").WithSafe("key", "notifications.email.categories."+ck)
				}
				if _, ok := cv.(bool); !ok {
					return fieldErr("notifications.email.categories."+ck, "must be a boolean")
				}
			}
		}
	}
	if qh, ok := asObject(n["quietHours"]); ok {
		for _, k := range []string{"start", "end", "tz"} {
			if v, has := qh[k]; has && v != nil {
				if _, ok := v.(string); !ok {
					return fieldErr("notifications.quietHours."+k, "must be a string")
				}
			}
		}
	}
	return nil
}

func validateStringArray(m map[string]any, key, path string) error {
	v, has := m[key]
	if !has || v == nil {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		if ss, ok := v.([]string); ok {
			if len(ss) > 100 {
				return fieldErr(path, "at most 100 items")
			}
			return nil
		}
		return fieldErr(path, "must be an array of strings")
	}
	if len(arr) > 100 {
		return fieldErr(path, "at most 100 items")
	}
	for i, x := range arr {
		if _, ok := x.(string); !ok {
			return fieldErr(fmt.Sprintf("%s/%d", path, i), "must be a string")
		}
	}
	return nil
}

func fieldErr(path, msg string) error {
	return errs.New(errs.CodeValidationFailed, "User settings validation failed.").WithSafe("path", path).WithSafe("message", msg)
}

// dashboardPeriods are the KPI periods of GET /v1/stats?period= (W74; internal/cluster kpiPeriods).
var dashboardPeriods = map[string]bool{"1h": true, "24h": true, "7d": true, "30d": true}

// validateDashboardPatch checks the dashboard section of a PATCH /v1/me/settings body: `dashboard` is an
// object (or null to clear it) and `dashboard.period` is 1h, 24h, 7d or 30d (or null). Only the incoming
// patch is checked, so a value stored before this rule never blocks unrelated changes.
func validateDashboardPatch(patch map[string]any) error {
	raw, has := patch["dashboard"]
	if !has || raw == nil {
		return nil
	}
	d, ok := asObject(raw)
	if !ok {
		return fieldErr("dashboard", "must be an object")
	}
	if p, has := d["period"]; has && p != nil {
		if ps, ok := p.(string); !ok || !dashboardPeriods[ps] {
			return fieldErr("dashboard.period", "must be one of 1h, 24h, 7d, 30d")
		}
	}
	return nil
}
