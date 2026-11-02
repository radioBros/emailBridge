package connectors

import (
	"errors"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestSSOPresetsExposeOIDCFields(t *testing.T) {
	want := map[string]string{
		TypeSSOAuthentik: "https://<authentik-host>/application/o/<slug>/",
		TypeSSOKeycloak:  "https://<keycloak-host>/realms/<realm>",
		TypeSSOEntra:     "https://login.microsoftonline.com/<tenant-id>/v2.0",
		TypeSSOGoogle:    "https://accounts.google.com",
		TypeSSOOkta:      "https://<okta-domain>/oauth2/default",
	}
	for _, d := range []Def{SSOAuthentik(), SSOKeycloak(), SSOEntra(), SSOGoogle(), SSOOkta()} {
		p, ok := OIDCPresetOf(d)
		if !ok {
			t.Fatalf("%s: missing preset", d.ID)
		}
		if p.IssuerURLPattern != want[d.ID] {
			t.Fatalf("%s issuer=%q want %q", d.ID, p.IssuerURLPattern, want[d.ID])
		}
		if len(p.Scopes) < 3 {
			t.Fatalf("%s scopes=%v", d.ID, p.Scopes)
		}
		if p.ButtonLabel == "" || p.Icon == "" || p.RedirectURIPath == "" || p.HelpText == "" {
			t.Fatalf("%s incomplete %+v", d.ID, p)
		}
		if p.RedirectURIPath != RedirectURIPathTemplate {
			t.Fatalf("%s redirect=%q", d.ID, p.RedirectURIPath)
		}
		if d.ID != TypeSSOGoogle && p.GroupClaim == "" {
			t.Fatalf("%s missing groupClaim", d.ID)
		}
	}
}

func TestOIDCPresetOfMissing(t *testing.T) {
	if _, ok := OIDCPresetOf(Def{}); ok {
		t.Fatal("expected false")
	}
	if _, ok := OIDCPresetOf(Def{ConfigSchema: map[string]any{}}); ok {
		t.Fatal("expected false")
	}
}

func TestOIDCPresetOfAltShapes(t *testing.T) {
	d := Def{ConfigSchema: map[string]any{
		OIDCPresetKey: map[string]any{
			"issuerUrlPattern": "https://x",
			"buttonLabel":      "X",
			"scopes":           []any{"openid", "email"},
			"claimMapping":     map[string]any{"groups": "groups"},
			"groupClaim":       "groups",
			"icon":             "mdi-x",
			"helpText":         "h",
			"redirectURIPath":  "/cb",
		},
	}}
	p, ok := OIDCPresetOf(d)
	if !ok || len(p.Scopes) != 2 || p.ClaimMapping["groups"] != "groups" {
		t.Fatalf("%+v ok=%v", p, ok)
	}
	d2 := Def{ConfigSchema: map[string]any{
		OIDCPresetKey: map[string]any{
			"issuerUrlPattern": "https://y",
			"buttonLabel":      "Y",
			"scopes":           []string{"openid"},
			"claimMapping":     map[string]string{"g": "g"},
		},
	}}
	p2, ok := OIDCPresetOf(d2)
	if !ok || len(p2.Scopes) != 1 || p2.ClaimMapping["g"] != "g" {
		t.Fatalf("%+v", p2)
	}
}

func TestGuardInstallSentry(t *testing.T) {
	d := Sentry()
	if !d.InstanceOwnerOnly {
		t.Fatal("sentry must be instance-owner only")
	}
	if err := GuardInstall(d, true); err != nil {
		t.Fatal(err)
	}
	err := GuardInstall(d, false)
	if err == nil || errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("got %v", err)
	}
	var ee *errs.E
	if !errors.As(err, &ee) || ee.Safe["reason"] != "instance_owner_required" {
		t.Fatalf("safe=%v", err)
	}
	if err := GuardInstall(Grafana(), false); err != nil {
		t.Fatal(err)
	}
}

func TestSentryRuntimeFrom(t *testing.T) {
	rt := SentryRuntimeFrom(map[string]any{
		"environment": "staging", "sampleRate": "0.5",
	}, map[string]string{"dsn": "https://k@h/1"}, true)
	if !rt.Enabled || rt.Environment != "staging" || rt.SampleRate != 0.5 || rt.DSN == "" {
		t.Fatalf("%+v", rt)
	}
	rt = SentryRuntimeFrom(nil, nil, true)
	if rt.Enabled {
		t.Fatal("empty dsn must disable")
	}
	rt = SentryRuntimeFrom(map[string]any{"sampleRate": "nope"}, map[string]string{"dsn": "x"}, false)
	if rt.Enabled || rt.SampleRate != 1.0 || rt.Environment != "production" {
		t.Fatalf("%+v", rt)
	}
}
