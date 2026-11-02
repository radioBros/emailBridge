package settings

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/config"
)

func TestParseValueKinds(t *testing.T) {
	cases := []struct {
		meta KeyMeta
		in   any
		ok   bool
	}{
		{KeyMeta{Kind: KindBool}, true, true},
		{KeyMeta{Kind: KindBool}, "true", true},
		{KeyMeta{Kind: KindBool}, 1, false},
		{KeyMeta{Kind: KindInt}, float64(3), true},
		{KeyMeta{Kind: KindInt}, "7", true},
		{KeyMeta{Kind: KindInt}, true, false},
		{KeyMeta{Kind: KindFloat}, float64(1.5), true},
		{KeyMeta{Kind: KindFloat}, "2.5", true},
		{KeyMeta{Kind: KindDuration}, "15m", true},
		{KeyMeta{Kind: KindSize}, "4KiB", true},
		{KeyMeta{Kind: KindSize}, float64(1024), true},
		{KeyMeta{Kind: KindRate}, "20/s,burst=40", true},
		{KeyMeta{Kind: KindStrings}, []any{"a", "b"}, true},
		{KeyMeta{Kind: KindStrings}, []string{"a"}, true},
		{KeyMeta{Kind: KindStrings}, "a,b", true},
		{KeyMeta{Kind: KindString, OneOf: []string{"a", "b"}}, "a", true},
		{KeyMeta{Kind: KindString, OneOf: []string{"a", "b"}}, "c", false},
		{KeyMeta{Kind: KindAny}, map[string]any{"x": 1}, true},
		{KeyMeta{Kind: KindBool}, nil, true},
	}
	for i, tc := range cases {
		_, err := ParseValue(tc.meta, tc.in)
		if (err == nil) != tc.ok {
			t.Fatalf("case %d meta=%v in=%v err=%v", i, tc.meta.Kind, tc.in, err)
		}
	}
	if _, ok := asInt(int64(3)); !ok {
		t.Fatal("asInt int64")
	}
	if _, ok := asInt(true); ok {
		t.Fatal("asInt bool")
	}
}

func TestDisplayBootValueAndMask(t *testing.T) {
	cfg := &config.Config{}
	cfg.Webhook.Timeout = 30 * time.Second
	cfg.Auth.SessionTTL = 12 * time.Hour
	cfg.SysMail.SMTPPass = "hunter2secret"
	cfg.Submit.MaxMessageSize = config.Size(25 << 20)
	cfg.Rate.APIKey = config.Rate{Count: 20, Period: time.Second, Burst: 40}
	cfg.Log.HashAddresses = true

	meta := KeyMeta{Name: "webhookTimeout", EnvKey: "EB_WEBHOOK_TIMEOUT", Kind: KindDuration}
	if v := DisplayBootValue(meta, cfg); v != "30s" {
		t.Fatalf("duration: %v", v)
	}
	meta = KeyMeta{Name: "sysmailSmtpPass", EnvKey: "EB_SYSMAIL_SMTP_PASS", Kind: KindString, Secret: true}
	if v, ok := DisplayBootValue(meta, cfg).(string); !ok || v != "••••cret" {
		t.Fatalf("secret mask: %v", DisplayBootValue(meta, cfg))
	}
	meta = KeyMeta{Name: "x", Kind: KindBool, Default: "false", RuntimeOnly: true}
	if v := DisplayBootValue(meta, nil); v != false {
		t.Fatalf("default: %v", v)
	}
	if maskSecret("") != "" || maskSecret("ab") != "••••ab" {
		t.Fatal("maskSecret")
	}
}

func TestNormalizeBSONShapes(t *testing.T) {
	m := normalizeMap(map[string]any{
		"a": normalizeValue([]any{1, map[string]any{"b": 2}}),
	})
	if m["a"] == nil {
		t.Fatal(m)
	}
}
