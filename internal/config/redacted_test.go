package config

import (
	"strings"
	"testing"
)

func TestRedacted(t *testing.T) {
	t.Parallel()
	cfg, _, err := Load(MapEnv{Vars: validBase()})
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Redacted()
	if m["EB_HTTP_ADDR"] != ":8080" {
		t.Fatalf("%v", m["EB_HTTP_ADDR"])
	}
	for _, k := range []string{"EB_MONGO_URI", "EB_AUTH_SECRET", "EB_KEYS_LOCAL", "EB_S3_ACCESS_KEY", "EB_SENTRY_DSN"} {
		if v, ok := m[k]; !ok || v != "[redacted]" {
			t.Fatalf("%s = %v", k, v)
		}
	}
	for _, v := range m {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if strings.Contains(s, "mongodb://") {
			t.Fatalf("mongo uri leaked: %s", s)
		}
	}
}
