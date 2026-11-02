package config

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestSizeStringAndKids(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s    Size
		want string
	}{
		{0, "0"},
		{1024, "1KiB"},
		{25 << 20, "25MiB"},
		{5 << 30, "5GiB"},
		{100, "100"},
	}
	for _, tc := range cases {
		if got := tc.s.String(); got != tc.want {
			t.Fatalf("%d -> %q want %q", tc.s, got, tc.want)
		}
	}
	kr := KeyRing{{Kid: "a"}, {Kid: "b"}}
	kids := kr.Kids()
	if len(kids) != 2 || kids[0] != "a" {
		t.Fatal(kids)
	}
	r := Rate{Count: 20, Period: time.Minute, Burst: 20}
	if r.String() != "20/min" {
		t.Fatal(r.String())
	}
	r = Rate{Count: 20, Period: time.Hour, Burst: 50}
	if r.String() != "20/h,burst=50" {
		t.Fatal(r.String())
	}
}

func TestLoadEmptyFileAndInvalidURL(t *testing.T) {
	t.Parallel()
	base := validBase()
	env := MapEnv{
		Vars:  map[string]string{},
		Files: map[string][]byte{"/empty": {}},
	}
	for k, v := range base {
		env.Vars[k] = v
	}
	env.Vars["EB_BOOTSTRAP_OWNER_PASSWORD_FILE"] = "/empty"
	cfg, report, err := Load(env)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.BootstrapOwnerPass != "" {
		t.Fatal("empty file should yield empty value")
	}
	if report.Sources["EB_BOOTSTRAP_OWNER_PASSWORD"] != SourceFile {
		t.Fatal(report.Sources["EB_BOOTSTRAP_OWNER_PASSWORD"])
	}

	base2 := validBase()
	base2["EB_PUBLIC_URL"] = "not a url"
	if _, _, err := Load(MapEnv{Vars: base2}); err == nil {
		t.Fatal("want url error")
	}

	base3 := validBase()
	base3["EB_HTTP_MAX_BODY"] = "nope"
	if _, _, err := Load(MapEnv{Vars: base3}); err == nil {
		t.Fatal("want size error")
	}

	base4 := validBase()
	base4["EB_NATS_REPLICAS"] = "0"
	if _, _, err := Load(MapEnv{Vars: base4}); err == nil {
		t.Fatal("want replicas error")
	}

	base5 := validBase()
	base5["EB_HTTP_TRUSTED_PROXIES"] = "10.0.0.0/8"
	base5["EB_HTTP_CORS_ORIGINS"] = "https://a.example, https://b.example"
	base5["EB_SYNC_CONNECT_RATE"] = "5/s,burst=10"
	base5["EB_AUTH_SECRET"] = string(bytes32(9)) // raw 32-byte secret
	cfg, _, err = Load(MapEnv{Vars: base5})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.HTTP.TrustedProxies) != 1 || len(cfg.HTTP.CORSOrigins) != 2 {
		t.Fatal(cfg.HTTP)
	}
	if cfg.Sync.ConnectRate.Burst != 10 {
		t.Fatal(cfg.Sync.ConnectRate)
	}
}

func TestMapEnvMissingFile(t *testing.T) {
	t.Parallel()
	env := MapEnv{Vars: map[string]string{"EB_AUTH_SECRET_FILE": "/missing"}}
	for k, v := range validBase() {
		if k == "EB_AUTH_SECRET" {
			continue
		}
		env.Vars[k] = v
	}
	if _, _, err := Load(env); err == nil {
		t.Fatal("want read error")
	}
	env2 := MapEnv{}
	if _, ok := env2.LookupEnv("x"); ok {
		t.Fatal("nil map")
	}
	if _, err := env2.ReadFile("x"); err == nil {
		t.Fatal("want missing")
	}
}

func TestLoadFromOSSmoke(t *testing.T) {
	key := bytes32(3)
	secret := bytes32(7)
	t.Setenv("EB_MONGO_URI", "mongodb://localhost:27017/?replicaSet=rs0")
	t.Setenv("EB_S3_ENDPOINT", "http://minio:9000")
	t.Setenv("EB_AUTH_SECRET", string(secret))
	t.Setenv("EB_KEYS_LOCAL", "kid1:"+base64.StdEncoding.EncodeToString(key))
	t.Setenv("EB_PUBLIC_URL", "https://mail.example.com")
	t.Setenv("EB_KEYS_PROVIDER", "local")
	t.Setenv("EB_NATS_MODE", "embedded")
	cfg, _, err := LoadFromOS()
	if err != nil {
		t.Skipf("LoadFromOS skipped due to ambient env: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" {
		t.Fatal(cfg.HTTP.Addr)
	}
}

func bytes32(seed byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}
