package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecretsInit(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := secretsCmd([]string{"init", "--dir", dir}, &out, &errb); code != 0 {
		t.Fatalf("init: code=%d err=%q", code, errb.String())
	}
	want := []string{
		"mongo_keyfile", "mongo_root_password", "mongo_uri",
		"s3_access_key", "s3_secret_key", "keys", "auth_secret",
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(got) != len(want) {
		t.Fatalf("wrote %d names, want %d: %q", len(got), len(want), out.String())
	}
	for i, name := range want {
		if got[i] != name {
			t.Fatalf("line %d: got %q want %q", i, got[i], name)
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" {
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("%s mode=%o want 0600", name, info.Mode().Perm())
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) == 0 {
			t.Fatalf("%s empty", name)
		}
		// Never echo secret values in test output; only check structure.
		if name == "mongo_uri" {
			pw, err := os.ReadFile(filepath.Join(dir, "mongo_root_password"))
			if err != nil {
				t.Fatal(err)
			}
			uri := string(data)
			if !strings.Contains(uri, string(pw)) {
				t.Fatal("mongo_uri must embed the generated root password")
			}
			if !strings.HasPrefix(uri, "mongodb://root:") || !strings.Contains(uri, "@mongo:27017/emailbridge?") {
				t.Fatalf("unexpected uri shape")
			}
			if !strings.Contains(uri, "authSource=admin") || !strings.Contains(uri, "replicaSet=rs0") {
				t.Fatalf("uri missing auth/replicaSet params")
			}
		}
		if name == "keys" && !strings.Contains(string(data), ":") {
			t.Fatal("keys must be kid:base64")
		}
	}
}

func TestSecretsInitNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := secretsCmd([]string{"init", "--dir", dir}, &out, &errb); code != 0 {
		t.Fatalf("first: %d %q", code, errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := secretsCmd([]string{"init", "--dir", dir}, &out, &errb); code != 1 {
		t.Fatalf("overwrite refused: code=%d err=%q", code, errb.String())
	}
	if !strings.Contains(errb.String(), "exists") {
		t.Fatalf("err=%q", errb.String())
	}
}

func TestSecretsInitForce(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := secretsCmd([]string{"init", "--dir", dir}, &out, &errb); code != 0 {
		t.Fatalf("first: %d %q", code, errb.String())
	}
	firstURI, _ := os.ReadFile(filepath.Join(dir, "mongo_uri"))
	out.Reset()
	errb.Reset()
	if code := secretsCmd([]string{"init", "--dir", dir, "--force"}, &out, &errb); code != 0 {
		t.Fatalf("force: %d %q", code, errb.String())
	}
	secondURI, _ := os.ReadFile(filepath.Join(dir, "mongo_uri"))
	if string(firstURI) == string(secondURI) {
		t.Fatal("--force must regenerate secrets")
	}
}

func TestSecretsUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := secretsCmd(nil, &out, &errb); code != 2 {
		t.Fatalf("nil args: %d", code)
	}
	if code := secretsCmd([]string{"nope"}, &out, &errb); code != 2 {
		t.Fatalf("bad sub: %d", code)
	}
	if code := secretsCmd([]string{"init", "--dir"}, &out, &errb); code != 2 {
		t.Fatalf("missing dir: %d", code)
	}
}

// TestSecretsInitSmoke is used by the P0.24 smoke check before main.go wires
// the "secrets" subcommand (Claude registers it). Set EB_SMOKE_SECRETS_DIR to a
// writable directory; otherwise the test skips.
func TestSecretsInitSmoke(t *testing.T) {
	dir := os.Getenv("EB_SMOKE_SECRETS_DIR")
	if dir == "" {
		t.Skip("EB_SMOKE_SECRETS_DIR not set")
	}
	var out, errb bytes.Buffer
	if code := secretsCmd([]string{"init", "--dir", dir, "--force"}, &out, &errb); code != 0 {
		t.Fatalf("smoke init: %d %q", code, errb.String())
	}
}
