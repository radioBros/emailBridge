package configcode

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// BuildPlan diffs desired against live and returns an ordered plan.
func BuildPlan(desired *Document, live *liveSnapshot, prune bool) (*Plan, error) {
	if err := validateDocument(desired); err != nil {
		return nil, err
	}
	if live == nil {
		live = &liveSnapshot{
			Settings: map[string]any{}, Switches: map[string]any{},
			Endpoints: map[string]liveEndpoint{}, Routes: map[string]liveRoute{}, APIKeys: map[string]liveAPIKey{},
		}
	}
	var actions []Action

	// 1. settings (single logical resource)
	if ch := diffSettings(desired.Spec.Settings, live.Settings); len(ch) > 0 {
		op := OpUpdate
		if live.SettingsV == 0 && len(live.Settings) == 0 {
			op = OpCreate
		}
		actions = append(actions, Action{
			Op: op, Kind: KindSettings, Name: "organization",
			Version: live.SettingsV, Changes: ch,
		})
	}

	// 2. switches — one action per changed switch name (stable order)
	swNames := sortedKeys(desired.Spec.Switches)
	for _, name := range swNames {
		want := desired.Spec.Switches[name]
		have, ok := live.Switches[name]
		if !ok || !equalJSON(want, have) {
			op := OpUpdate
			if !ok {
				op = OpCreate
			}
			actions = append(actions, Action{
				Op: op, Kind: KindSwitch, Name: name, Version: live.SettingsV,
				Changes: map[string]any{"value": fieldChange(have, want)},
			})
		}
	}
	if prune {
		for _, name := range sortedKeys(live.Switches) {
			if _, ok := desired.Spec.Switches[name]; ok {
				continue
			}
			actions = append(actions, Action{
				Op: OpDelete, Kind: KindSwitch, Name: name, Version: live.SettingsV,
			})
		}
	}

	// 3. endpoints — creates/updates first
	desEP := indexEndpoints(desired.Spec.WebhookEndpoints)
	for _, name := range sortedKeys(desEP) {
		want := desEP[name]
		have, ok := live.Endpoints[name]
		if !ok {
			actions = append(actions, Action{
				Op: OpCreate, Kind: KindEndpoint, Name: name,
				Changes: endpointCreateChanges(want),
			})
			continue
		}
		if ch := diffEndpoint(want, have); len(ch) > 0 {
			actions = append(actions, Action{
				Op: OpUpdate, Kind: KindEndpoint, Name: name, ID: have.ID, Version: have.Version, Changes: ch,
			})
		}
	}

	// 4. routes
	desRT := indexRoutes(desired.Spec.Routes)
	for _, name := range sortedKeys(desRT) {
		want := desRT[name]
		have, ok := live.Routes[name]
		if !ok {
			actions = append(actions, Action{
				Op: OpCreate, Kind: KindRoute, Name: name,
				Changes: routeCreateChanges(want),
			})
			continue
		}
		if ch := diffRoute(want, have); len(ch) > 0 {
			actions = append(actions, Action{
				Op: OpUpdate, Kind: KindRoute, Name: name, ID: have.ID, Version: have.Version, Changes: ch,
			})
		}
	}

	// 5. api keys
	desKey := indexAPIKeys(desired.Spec.APIKeys)
	for _, name := range sortedKeys(desKey) {
		want := desKey[name]
		have, ok := live.APIKeys[name]
		if !ok {
			actions = append(actions, Action{
				Op: OpCreate, Kind: KindAPIKey, Name: name,
				Changes: apiKeyCreateChanges(want),
			})
			continue
		}
		if ch := diffAPIKey(want, have.Spec); len(ch) > 0 {
			actions = append(actions, Action{
				Op: OpUpdate, Kind: KindAPIKey, Name: name, ID: have.ID, Version: have.Version, Changes: ch,
			})
		}
	}

	// 6. deletes (reverse dependency: keys, routes, endpoints, then pruned settings keys)
	if prune {
		for _, name := range sortedKeys(live.APIKeys) {
			if _, ok := desKey[name]; !ok {
				k := live.APIKeys[name]
				actions = append(actions, Action{Op: OpDelete, Kind: KindAPIKey, Name: name, ID: k.ID, Version: k.Version})
			}
		}
		for _, name := range sortedKeys(live.Routes) {
			if _, ok := desRT[name]; !ok {
				r := live.Routes[name]
				actions = append(actions, Action{Op: OpDelete, Kind: KindRoute, Name: name, ID: r.ID, Version: r.Version})
			}
		}
		for _, name := range sortedKeys(live.Endpoints) {
			if _, ok := desEP[name]; !ok {
				e := live.Endpoints[name]
				actions = append(actions, Action{Op: OpDelete, Kind: KindEndpoint, Name: name, ID: e.ID, Version: e.Version})
			}
		}
	}

	hash, err := HashPlan(prune, actions)
	if err != nil {
		return nil, err
	}
	return &Plan{PlanHash: hash, Prune: prune, Actions: actions}, nil
}

func diffSettings(want, have map[string]any) map[string]any {
	if want == nil {
		want = map[string]any{}
	}
	if have == nil {
		have = map[string]any{}
	}
	ch := map[string]any{}
	for _, k := range sortedKeys(want) {
		if !equalJSON(want[k], have[k]) {
			ch[k] = fieldChange(have[k], want[k])
		}
	}
	return ch
}

func diffEndpoint(want EndpointSpec, have liveEndpoint) map[string]any {
	ch := map[string]any{}
	live := have.Spec
	if strings.TrimSpace(want.URL) != strings.TrimSpace(live.URL) {
		ch["url"] = fieldChange(live.URL, want.URL)
	}
	wantKind := want.Kind
	if wantKind == "" {
		wantKind = "http"
	}
	liveKind := live.Kind
	if liveKind == "" {
		liveKind = "http"
	}
	if wantKind != liveKind {
		ch["kind"] = fieldChange(liveKind, wantKind)
	}
	if want.Enabled != nil {
		liveEn := true
		if live.Enabled != nil {
			liveEn = *live.Enabled
		}
		if *want.Enabled != liveEn {
			ch["enabled"] = fieldChange(liveEn, *want.Enabled)
		}
	}
	if !equalJSON(want.Events, live.Events) {
		ch["events"] = fieldChange(live.Events, want.Events)
	}
	if want.Filter != nil && !equalJSON(want.Filter, live.Filter) {
		ch["filter"] = fieldChange(live.Filter, want.Filter)
	}
	if want.Payload != nil && !equalJSON(want.Payload, live.Payload) {
		ch["payload"] = fieldChange(live.Payload, want.Payload)
	}
	if want.Retry != nil && !equalJSON(want.Retry, live.Retry) {
		ch["retry"] = fieldChange(live.Retry, want.Retry)
	}
	if want.Delivery != nil && !equalJSON(want.Delivery, live.Delivery) {
		ch["delivery"] = fieldChange(live.Delivery, want.Delivery)
	}
	if want.Security != nil && want.Security.SignatureMode != "" {
		liveMode := ""
		if live.Security != nil {
			liveMode = live.Security.SignatureMode
		}
		if want.Security.SignatureMode != liveMode {
			ch["security.signatureMode"] = fieldChange(liveMode, want.Security.SignatureMode)
		}
	}
	if want.Switches != nil && !equalJSON(want.Switches, live.Switches) {
		ch["switches"] = fieldChange(live.Switches, want.Switches)
	}
	if want.SinkConfig != nil && !equalJSON(want.SinkConfig, live.SinkConfig) {
		ch["sinkConfig"] = fieldChange(live.SinkConfig, want.SinkConfig)
	}
	if want.Secret != "" {
		// Presence of a secret ref always schedules an update; value never shown.
		ch["secret"] = redactSecretChange(true)
	} else if have.HasSecret {
		ch["secret"] = redactSecretChange(false) // listed only when other fields change — drop if sole
		if len(ch) == 1 {
			delete(ch, "secret")
		}
	}
	return ch
}

func diffRoute(want RouteSpec, have liveRoute) map[string]any {
	ch := map[string]any{}
	live := have.Spec
	if want.Endpoint != have.EndpointName && want.Endpoint != live.Endpoint {
		ch["endpoint"] = fieldChange(have.EndpointName, want.Endpoint)
	}
	if want.Enabled != nil {
		liveEn := true
		if live.Enabled != nil {
			liveEn = *live.Enabled
		}
		if *want.Enabled != liveEn {
			ch["enabled"] = fieldChange(liveEn, *want.Enabled)
		}
	}
	if want.Filter != live.Filter {
		ch["filter"] = fieldChange(live.Filter, want.Filter)
	}
	if want.Map != live.Map {
		ch["map"] = fieldChange(live.Map, want.Map)
	}
	if want.Order != nil {
		liveOrd := 0
		if live.Order != nil {
			liveOrd = *live.Order
		}
		if *want.Order != liveOrd {
			ch["order"] = fieldChange(liveOrd, *want.Order)
		}
	}
	if want.Headers != nil && !equalJSON(want.Headers, live.Headers) {
		ch["headers"] = fieldChange(live.Headers, want.Headers)
	}
	if want.Payload != nil && !equalJSON(want.Payload, live.Payload) {
		ch["payload"] = fieldChange(live.Payload, want.Payload)
	}
	if want.Builder != nil && !equalJSON(want.Builder, live.Builder) {
		ch["builder"] = fieldChange(live.Builder, want.Builder)
	}
	if want.Secret != "" {
		ch["secret"] = redactSecretChange(true)
	}
	return ch
}

func diffAPIKey(want, have APIKeySpec) map[string]any {
	ch := map[string]any{}
	if !equalJSON(want.Scopes, have.Scopes) {
		ch["scopes"] = fieldChange(have.Scopes, want.Scopes)
	}
	if want.AccountID != have.AccountID {
		ch["accountId"] = fieldChange(have.AccountID, want.AccountID)
	}
	if !equalJSON(want.Restrictions, have.Restrictions) {
		ch["restrictions"] = fieldChange(have.Restrictions, want.Restrictions)
	}
	if !equalJSON(want.RateLimit, have.RateLimit) {
		ch["rateLimit"] = fieldChange(have.RateLimit, want.RateLimit)
	}
	if !equalJSON(want.ExpiresAt, have.ExpiresAt) {
		ch["expiresAt"] = fieldChange(have.ExpiresAt, want.ExpiresAt)
	}
	return ch
}

func endpointCreateChanges(e EndpointSpec) map[string]any {
	m := map[string]any{"url": e.URL, "events": e.Events}
	if e.Kind != "" {
		m["kind"] = e.Kind
	}
	if e.Enabled != nil {
		m["enabled"] = *e.Enabled
	}
	if e.Secret != "" {
		m["secret"] = redactSecretChange(true)
	}
	return m
}

func routeCreateChanges(r RouteSpec) map[string]any {
	m := map[string]any{"endpoint": r.Endpoint, "filter": r.Filter}
	if r.Map != "" {
		m["map"] = r.Map
	}
	if r.Secret != "" {
		m["secret"] = redactSecretChange(true)
	}
	return m
}

func apiKeyCreateChanges(k APIKeySpec) map[string]any {
	return map[string]any{"scopes": k.Scopes}
}

func fieldChange(before, after any) map[string]any {
	return map[string]any{"before": before, "after": after}
}

func equalJSON(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	ab, err1 := json.Marshal(a)
	bb, err2 := json.Marshal(b)
	if err1 != nil || err2 != nil {
		return false
	}
	return string(ab) == string(bb)
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func indexEndpoints(list []EndpointSpec) map[string]EndpointSpec {
	out := make(map[string]EndpointSpec, len(list))
	for _, e := range list {
		out[e.Name] = e
	}
	return out
}

func indexRoutes(list []RouteSpec) map[string]RouteSpec {
	out := make(map[string]RouteSpec, len(list))
	for _, r := range list {
		out[r.Name] = r
	}
	return out
}

func indexAPIKeys(list []APIKeySpec) map[string]APIKeySpec {
	out := make(map[string]APIKeySpec, len(list))
	for _, k := range list {
		out[k.Name] = k
	}
	return out
}
