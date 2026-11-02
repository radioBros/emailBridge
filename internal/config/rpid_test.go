package config

import (
	"strings"
	"testing"
)

// An IP public URL must not stop the node (the passkey library refuses an IP relying-party ID): the derived RP ID
// falls back to localhost with a warning; an explicit IP RP ID is a configuration error.
func TestWebAuthnRPIDNeverAnIP(t *testing.T) {
	env := validBase()
	env["EB_PUBLIC_URL"] = "http://127.0.0.1:8080"
	env["EB_ENV"] = "development"
	cfg, report, err := Load(MapEnv{Vars: env})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.WebAuthnRPID != "localhost" {
		t.Fatalf("RP ID %q", cfg.Auth.WebAuthnRPID)
	}
	found := false
	for _, w := range report.Warnings {
		found = found || strings.Contains(w, "passkeys are unavailable")
	}
	if !found {
		t.Fatalf("warnings %v", report.Warnings)
	}

	env["EB_AUTH_WEBAUTHN_RP_ID"] = "10.0.0.5"
	if _, _, err := Load(MapEnv{Vars: env}); err == nil || !strings.Contains(err.Error(), "EB_AUTH_WEBAUTHN_RP_ID") {
		t.Fatalf("explicit IP RP ID: %v", err)
	}

	base := validBase()
	cfg, _, err = Load(MapEnv{Vars: base})
	if err != nil || cfg.Auth.WebAuthnRPID != "mail.example.com" {
		t.Fatalf("domain RP ID: %q %v", cfg.Auth.WebAuthnRPID, err)
	}
}
