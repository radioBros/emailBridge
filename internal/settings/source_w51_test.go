package settings

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/features"
)

func TestFeatureSourceLockedOrganization(t *testing.T) {
	t.Parallel()
	if featureSource(features.SourceLockedOrganization) != "organization" {
		t.Fatal(featureSource(features.SourceLockedOrganization))
	}
	if featureSource(features.SourceLockedGlobal) != "global" {
		t.Fatal(featureSource(features.SourceLockedGlobal))
	}
	if featureSource(features.SourceLower) != "account" {
		t.Fatal(featureSource(features.SourceLower))
	}
	if featureSource(features.SourceDefault) != "default" {
		t.Fatal(featureSource(features.SourceDefault))
	}
}

func TestToFeatureValueAndDisplay(t *testing.T) {
	t.Parallel()
	strSW := features.Switch{Default: features.StringValue("standard")}
	if v := toFeatureValue(strSW, "bodyOnly"); v.String == nil || *v.String != "bodyOnly" {
		t.Fatalf("%v", v)
	}
	if v := toFeatureValue(strSW, 42); v.String == nil || *v.String != "42" {
		t.Fatalf("%v", v)
	}
	boolSW := features.Switch{Default: features.BoolValue(false)}
	if v := toFeatureValue(boolSW, true); v.Bool == nil || !*v.Bool {
		t.Fatalf("%v", v)
	}
	if v := toFeatureValue(boolSW, "x"); v.Bool == nil || *v.Bool {
		t.Fatalf("%v", v)
	}
	if featureDisplay(features.BoolValue(true)) != true {
		t.Fatal("bool display")
	}
	if featureDisplay(features.StringValue("a")) != "a" {
		t.Fatal("string display")
	}
	if featureDisplay(features.Value{}) != nil {
		t.Fatal("empty display")
	}
	if switchChangeScope(features.Switch{Scopes: []features.Scope{features.ScopeGlobal}}) != "global" {
		t.Fatal("global change scope")
	}
	if switchChangeScope(features.Switch{Scopes: []features.Scope{features.ScopeOrganization, features.ScopeAccount}}) != "organization" {
		t.Fatal("org change scope")
	}
}
