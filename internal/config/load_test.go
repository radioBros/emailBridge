package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func validBase() map[string]string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i + 40)
	}
	return map[string]string{
		"EB_MONGO_URI":   "mongodb://localhost:27017/?replicaSet=rs0",
		"EB_S3_ENDPOINT": "http://minio:9000",
		"EB_AUTH_SECRET": base64.StdEncoding.EncodeToString(secret),
		"EB_KEYS_LOCAL":  "kid1:" + base64.StdEncoding.EncodeToString(key),
		"EB_PUBLIC_URL":  "https://mail.example.com",
		"EB_ENV":         "production",
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	cfg, report, err := Load(MapEnv{Vars: validBase()})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Fatalf("addr %q", cfg.HTTP.Addr)
	}
	if report.Sources["EB_HTTP_ADDR"] != SourceDefault {
		t.Fatalf("source %v", report.Sources["EB_HTTP_ADDR"])
	}
	if cfg.HTTP.MaxBody != 32<<20 {
		t.Fatalf("max body %v", cfg.HTTP.MaxBody)
	}
	if cfg.Rate.APIKey.Burst != 40 {
		t.Fatalf("rate %+v", cfg.Rate.APIKey)
	}
	if cfg.Keys.Active != "kid1" {
		t.Fatalf("active %q", cfg.Keys.Active)
	}
	if cfg.Lease.LeaseRenew != 10*time.Second {
		t.Fatalf("lease renew %v", cfg.Lease.LeaseRenew)
	}
	if cfg.Log.Dir != "/data/logs" {
		t.Fatalf("log dir %q", cfg.Log.Dir)
	}
}

func TestLoadFileAndBothSet(t *testing.T) {
	t.Parallel()
	base := validBase()
	delete(base, "EB_AUTH_SECRET")
	secret := make([]byte, 32)
	env := MapEnv{
		Vars: map[string]string{},
		Files: map[string][]byte{
			"/secret": append([]byte(base64.StdEncoding.EncodeToString(secret)), '\n'),
		},
	}
	for k, v := range base {
		env.Vars[k] = v
	}
	env.Vars["EB_AUTH_SECRET_FILE"] = "/secret"
	cfg, report, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if report.Sources["EB_AUTH_SECRET"] != SourceFile {
		t.Fatalf("source %v", report.Sources["EB_AUTH_SECRET"])
	}
	if got := cfg.Auth.Secret; got != base64.StdEncoding.EncodeToString(secret) {
		t.Fatalf("secret mismatch %q", got)
	}

	// CRLF trim
	env.Files["/secret"] = append([]byte(base64.StdEncoding.EncodeToString(secret)), '\r', '\n')
	cfg, _, err = Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.Secret != base64.StdEncoding.EncodeToString(secret) {
		t.Fatal("CRLF not trimmed")
	}

	// both set
	env.Vars["EB_AUTH_SECRET"] = base64.StdEncoding.EncodeToString(secret)
	_, _, err = Load(env)
	if err == nil || !strings.Contains(err.Error(), "EB_AUTH_SECRET") {
		t.Fatalf("want both-set error, got %v", err)
	}
}

func TestLoadUnknownKeyWarning(t *testing.T) {
	t.Parallel()
	vars := validBase()
	vars["EB_TYPO_THING"] = "1"
	vars["EB_TEST_MAIL_USER"] = "x" // ignored
	_, report, err := Load(MapEnv{Vars: vars})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "EB_TYPO_THING") {
			found = true
		}
		if strings.Contains(w, "EB_TEST_") {
			t.Fatalf("should not warn on test key: %s", w)
		}
	}
	if !found {
		t.Fatalf("warnings %v", report.Warnings)
	}
}

func TestLoadValidationRules(t *testing.T) {
	t.Parallel()
	t.Run("required", func(t *testing.T) {
		t.Parallel()
		_, _, err := Load(MapEnv{Vars: map[string]string{}})
		if err == nil {
			t.Fatal("want error")
		}
		msg := err.Error()
		for _, k := range []string{"EB_MONGO_URI", "EB_S3_ENDPOINT", "EB_AUTH_SECRET", "EB_KEYS_LOCAL"} {
			if !strings.Contains(msg, k) {
				t.Fatalf("missing %s in %s", k, msg)
			}
		}
	})
	t.Run("nats external", func(t *testing.T) {
		t.Parallel()
		vars := validBase()
		vars["EB_NATS_MODE"] = "external"
		_, _, err := Load(MapEnv{Vars: vars})
		if err == nil || !strings.Contains(err.Error(), "EB_NATS_URL") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("cookie secure", func(t *testing.T) {
		t.Parallel()
		vars := validBase()
		vars["EB_AUTH_COOKIE_SECURE"] = "false"
		_, _, err := Load(MapEnv{Vars: vars})
		if err == nil || !strings.Contains(err.Error(), "EB_AUTH_COOKIE_SECURE") {
			t.Fatalf("%v", err)
		}
		vars["EB_ENV"] = "development"
		if _, _, err := Load(MapEnv{Vars: vars}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("sysmail tls none", func(t *testing.T) {
		t.Parallel()
		vars := validBase()
		vars["EB_SYSMAIL_SMTP_TLS"] = "none"
		_, _, err := Load(MapEnv{Vars: vars})
		if err == nil || !strings.Contains(err.Error(), "EB_SYSMAIL_SMTP_TLS") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("oneof", func(t *testing.T) {
		t.Parallel()
		vars := validBase()
		vars["EB_ENV"] = "nope"
		_, _, err := Load(MapEnv{Vars: vars})
		if err == nil || !strings.Contains(err.Error(), "EB_ENV") {
			t.Fatalf("%v", err)
		}
	})
	t.Run("auth secret length", func(t *testing.T) {
		t.Parallel()
		vars := validBase()
		sentinel := "SENTINEL_BAD_SECRET_TOO_SHORT"
		vars["EB_AUTH_SECRET"] = sentinel
		_, _, err := Load(MapEnv{Vars: vars})
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("secret leaked: %v", err)
		}
	})
	t.Run("public url validate", func(t *testing.T) {
		t.Parallel()
		cfg, _, err := Load(MapEnv{Vars: validBase()})
		if err != nil {
			t.Fatal(err)
		}
		if err := cfg.Validate(true); err != nil {
			t.Fatal(err)
		}
		cfg.HTTP.PublicURL = ""
		if err := cfg.Validate(true); err == nil {
			t.Fatal("want public url required")
		}
		if err := cfg.Validate(false); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSecretNotInKeyRingErrors(t *testing.T) {
	t.Parallel()
	vars := validBase()
	sentinel := "kid1:SENTINEL_NOT_VALID_BASE64_!!!!!!!"
	vars["EB_KEYS_LOCAL"] = sentinel
	_, _, err := Load(MapEnv{Vars: vars})
	if err == nil {
		t.Fatal("want error")
	}
	if strings.Contains(err.Error(), "SENTINEL") {
		t.Fatalf("secret leaked: %v", err)
	}
}
