package settings

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/features"
)

func TestDeepMergeNullDeletesAndObjectsMerge(t *testing.T) {
	base := map[string]any{
		"tables": map[string]any{
			"accounts": map[string]any{
				"visibility": []any{"name", "email"},
				"order":      []any{"name"},
			},
		},
		"ui": map[string]any{"theme": "light", "locale": "it"},
	}
	patch := map[string]any{
		"tables": map[string]any{
			"accounts": map[string]any{
				"order": []any{"email", "name"},
			},
		},
		"ui": map[string]any{
			"theme":  "dark",
			"locale": nil, // delete
		},
	}
	out, err := DeepMerge(base, patch)
	if err != nil {
		t.Fatal(err)
	}
	tables := out["tables"].(map[string]any)["accounts"].(map[string]any)
	vis := tables["visibility"].([]any)
	if len(vis) != 2 || vis[0] != "name" {
		t.Fatalf("visibility should be preserved: %v", vis)
	}
	ord := tables["order"].([]any)
	if len(ord) != 2 || ord[0] != "email" {
		t.Fatalf("order replaced: %v", ord)
	}
	ui := out["ui"].(map[string]any)
	if ui["theme"] != "dark" {
		t.Fatalf("theme: %v", ui["theme"])
	}
	if _, ok := ui["locale"]; ok {
		t.Fatal("locale should be deleted")
	}
}

func TestDeepMergeRejectsPollution(t *testing.T) {
	_, err := DeepMerge(map[string]any{}, map[string]any{"__proto__": map[string]any{"x": 1}})
	if err == nil {
		t.Fatal("expected pollution error")
	}
}

func TestMongoUnsafeKeys(t *testing.T) {
	cases := []struct {
		name string
		key  string
		bad  bool
	}{
		{"empty", "", true},
		{"dollar", "$gt", true},
		{"dot", "a.b", true},
		{"nul", "a\x00b", true},
		{"ok", "theme", false},
		{"ok_underscore", "my_key", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMongoUnsafeKey(tc.key); got != tc.bad {
				t.Fatalf("isMongoUnsafeKey(%q)=%v want %v", tc.key, got, tc.bad)
			}
			patch := map[string]any{tc.key: "x"}
			err := rejectPollution(patch, "")
			if tc.bad && err == nil {
				t.Fatal("rejectPollution expected error")
			}
			if !tc.bad && err != nil {
				t.Fatalf("rejectPollution: %v", err)
			}
			_, err = DeepMerge(map[string]any{}, patch)
			if tc.bad && err == nil {
				t.Fatal("DeepMerge expected error")
			}
			if !tc.bad && err != nil {
				t.Fatalf("DeepMerge: %v", err)
			}
			err = ValidateUserSettings(patch)
			if tc.bad && err == nil {
				t.Fatal("ValidateUserSettings expected error")
			}
			if !tc.bad && err != nil {
				t.Fatalf("ValidateUserSettings: %v", err)
			}
		})
	}
	// nested
	if err := rejectPollution(map[string]any{"ui": map[string]any{"$where": 1}}, ""); err == nil {
		t.Fatal("nested $")
	}
	// known dotted setting key allowed via allow-list
	s := NewService(nil, nil, features.NewRegistry(), nil)
	if err := rejectClientKeys(map[string]any{"sandbox.rewriteTo": "a@b.c"}, "", s.allowSettingsDottedKey); err != nil {
		t.Fatalf("catalog dotted key: %v", err)
	}
	if err := rejectClientKeys(map[string]any{"env": map[string]any{"a.b": 1}}, "", s.allowSettingsDottedKey); err == nil {
		t.Fatal("env dotted key must fail")
	}
}

func TestValidateUserSettings(t *testing.T) {
	ok := map[string]any{
		"tables": map[string]any{
			"accounts": map[string]any{
				"visibility": []any{"a"},
				"order":      []any{"a"},
				"widths":     map[string]any{"a": float64(10)},
			},
		},
		"ui": map[string]any{"theme": "dark", "density": "compact", "locale": "en"},
	}
	if err := ValidateUserSettings(ok); err != nil {
		t.Fatal(err)
	}
	bad := map[string]any{"tables": map[string]any{"x": map[string]any{"visibility": "nope"}}}
	if err := ValidateUserSettings(bad); err == nil {
		t.Fatal("expected validation error")
	}
	huge := map[string]any{"pad": strings.Repeat("x", maxUserSettingsBytes)}
	if err := ValidateUserSettings(huge); err == nil {
		t.Fatal("expected size error")
	}
}

func TestValidateUserSettingsLocale(t *testing.T) {
	for _, code := range []string{"en", "it", "es", "fr", "de", "pt", "ru", "zh", "ja"} {
		if err := ValidateUserSettings(map[string]any{"ui": map[string]any{"locale": code}}); err != nil {
			t.Fatalf("%s: %v", code, err)
		}
	}
	err := ValidateUserSettings(map[string]any{"ui": map[string]any{"locale": "xx"}})
	if err == nil {
		t.Fatal("expected rejection of unknown locale")
	}
	if !strings.Contains(err.Error(), "validation") && err.Error() == "" {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestEnvToCamel(t *testing.T) {
	if got := envToCamel("EB_WEBHOOK_TIMEOUT"); got != "webhookTimeout" {
		t.Fatalf("got %q", got)
	}
	if got := envToCamel("EB_AUTH_SESSION_TTL"); got != "authSessionTtl" {
		t.Fatalf("got %q", got)
	}
}

func TestCatalogHasRuntimeKeys(t *testing.T) {
	c := Catalog()
	if _, ok := c["webhookTimeout"]; !ok {
		t.Fatal("missing webhookTimeout")
	}
	if _, ok := c["sandbox.rewriteTo"]; !ok {
		t.Fatal("missing runtime-only key")
	}
	if meta := c["sysmailSmtpPass"]; !meta.Secret {
		// may be named differently
		found := false
		for _, m := range c {
			if m.Secret && m.EnvKey == "EB_SYSMAIL_SMTP_PASS" {
				found = true
				break
			}
		}
		if !found {
			t.Fatal("secret sysmail pass not in catalog")
		}
	}
}

func TestParseValueDurationSize(t *testing.T) {
	m := KeyMeta{Kind: KindDuration}
	v, err := ParseValue(m, "5d")
	if err != nil || v != "5d" {
		t.Fatalf("%v %v", v, err)
	}
	m = KeyMeta{Kind: KindSize}
	v, err = ParseValue(m, "10MiB")
	if err != nil || v != "10MiB" {
		t.Fatalf("%v %v", v, err)
	}
}

func TestSourcePrecedenceResolve(t *testing.T) {
	s := NewService(nil, nil, features.NewRegistry(), nil)
	global := &Doc{ID: DocIDGlobal, Values: map[string]any{"webhookTimeout": "10s"}, Switches: map[string]SwitchValue{}, Version: 1}
	org := &Doc{ID: OrgDocID("org_x"), Values: map[string]any{"webhookTimeout": "20s"}, Switches: map[string]SwitchValue{}, Version: 2}
	body := s.resolve(global, org)
	sv := body.Values["webhookTimeout"]
	if sv.Source != "organization" || sv.Value != "20s" {
		t.Fatalf("%+v", sv)
	}
	body = s.resolve(global, &Doc{ID: OrgDocID("org_x"), Values: map[string]any{}, Switches: map[string]SwitchValue{}})
	sv = body.Values["webhookTimeout"]
	if sv.Source != "global" || sv.Value != "10s" {
		t.Fatalf("global: %+v", sv)
	}
}

func TestValidateMoreAndHelpers(t *testing.T) {
	if err := ValidateUserSettings(map[string]any{
		"tables": map[string]any{"t": map[string]any{"order": []string{"a", "b"}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateUserSettings(map[string]any{
		"tables": map[string]any{"t": map[string]any{"widths": "no"}},
	}); err == nil {
		t.Fatal("widths")
	}
	if err := ValidateUserSettings(map[string]any{
		"ui": map[string]any{"theme": 1},
	}); err == nil {
		t.Fatal("ui theme")
	}
	if err := ValidateUserSettings(map[string]any{"__proto__": 1}); err == nil {
		t.Fatal("pollution")
	}
	long := make([]any, 101)
	for i := range long {
		long[i] = "x"
	}
	if err := ValidateUserSettings(map[string]any{
		"tables": map[string]any{"t": map[string]any{"visibility": long}},
	}); err == nil {
		t.Fatal("max items")
	}
	if err := rejectPollution(map[string]any{"a": map[string]any{"constructor": 1}}, ""); err == nil {
		t.Fatal("nested pollution")
	}
	if _, ok := asObject(bson.M{"a": 1}); !ok {
		t.Fatal("bson.M asObject")
	}
	_ = normalizeValue(bson.A{1, 2})
	_ = normalizeValue(bson.M{"a": bson.D{{Key: "b", Value: 1}}})
	if trimETag(`"v3"`) != "v3" || parseETag("12") != 12 {
		t.Fatal("etag")
	}
	m := &Module{}
	if len(m.Roles()) != 1 {
		t.Fatal("roles")
	}
	if changeScope(KeyMeta{Scope: "global"}) != "global" || changeScope(KeyMeta{Scope: "organization"}) != "organization" {
		t.Fatal("changeScope")
	}
	if featureDisplay(features.Value{}) != nil {
		t.Fatal("empty feature")
	}
	if featureSource(features.SourceLower) != "account" {
		t.Fatal("lower source")
	}
	if _, ok := asSecretStored(secretStored{Secret: true, Last4: "abcd"}); !ok {
		t.Fatal("secretStored typed")
	}
	sw := features.Switch{Default: features.StringValue("a")}
	if toFeatureValue(sw, "b").String == nil {
		t.Fatal("string switch")
	}
	sw = features.Switch{Default: features.BoolValue(false)}
	if toFeatureValue(sw, true).Bool == nil {
		t.Fatal("bool switch")
	}
}

func TestApplyEnvPatchUnit(t *testing.T) {
	s := NewService(nil, nil, features.NewRegistry(), nil)
	doc := &Doc{Env: map[string]any{}}
	if err := s.applyEnvPatch(context.Background(), doc, "nope"); err == nil {
		t.Fatal("want error")
	}
	if err := s.applyEnvPatch(context.Background(), doc, nil); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyScopeInvalidate(t *testing.T) {
	now := time.Now()
	entry := func() *cachedEffective {
		return &cachedEffective{body: SettingsBody{Version: 1}, at: now}
	}

	// Org notify scope "org:<id>" must evict only that org on a remote Service.
	remote := NewService(nil, nil, features.NewRegistry(), nil)
	remote.cache["orgA"] = entry()
	remote.cache["orgB"] = entry()
	remote.cache[""] = entry()
	remote.Invalidate(OrgDocID("orgA")) // what Patch notifies for an org change
	if _, ok := remote.cache["orgA"]; ok {
		t.Fatal("orgA should be evicted")
	}
	if _, ok := remote.cache["orgB"]; !ok {
		t.Fatal("orgB should remain")
	}
	if _, ok := remote.cache[""]; !ok {
		t.Fatal("global cache entry should remain after org invalidate")
	}

	// Global notify scope clears every cached effective view.
	remote.Invalidate("global")
	if len(remote.cache) != 0 {
		t.Fatalf("global invalidate should clear all, left %v", remote.cache)
	}
}
