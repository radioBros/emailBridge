package integrations

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestValidateConfigSlack(t *testing.T) {
	def := slackDefinition()
	cfg := map[string]any{"webhookUrl": "https://hooks.slack.com/services/T/B/X"}
	applySchemaDefaults(def, cfg)
	if err := validateConfig(def.ConfigSchema, cfg, nil, def.SecretFields); err != nil {
		t.Fatal(err)
	}
	if err := validateConfig(def.ConfigSchema, map[string]any{}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("missing: %v", err)
	}
	if err := validateConfig(def.ConfigSchema, map[string]any{
		"webhookUrl": "not-a-url",
	}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad uri: %v", err)
	}
	if err := validateConfig(def.ConfigSchema, map[string]any{
		"webhookUrl": "https://hooks.slack.com/x", "nope": 1,
	}, nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("unknown: %v", err)
	}
}

func TestValidateConfigRspamdSecrets(t *testing.T) {
	def := RspamdDefinition()
	cfg := map[string]any{"baseUrl": "http://rspamd:11333", "timeout": "5s"}
	if err := validateConfig(def.ConfigSchema, cfg, map[string]string{"password": "secret"}, def.SecretFields); err != nil {
		t.Fatal(err)
	}
	cfg2, sec := splitConfigSecrets(def, map[string]any{
		"baseUrl": "http://r", "password": "p",
	}, nil)
	if cfg2["password"] != nil || sec["password"] != "p" {
		t.Fatalf("%v %v", cfg2, sec)
	}
}

func TestResolveTemplate(t *testing.T) {
	got := resolveTemplate("hi {{config.name}} / {{secrets.token}}",
		map[string]any{"name": "Ada"}, map[string]string{"token": "t1"})
	if got != "hi Ada / t1" {
		t.Fatal(got)
	}
}

func TestRegistryAndSwitchName(t *testing.T) {
	r := DefaultRegistry()
	// zapier + rspamd + 7 P8.2 + P8.3 catalogue
	n := len(r.All())
	if n < 9 {
		t.Fatalf("got %d (want ≥9)", n)
	}
	if _, ok := r.Get(TypeFeatherDesk); !ok {
		t.Fatal("featherdesk")
	}
	if _, ok := r.Get(TypeSlack); !ok {
		t.Fatal("slack")
	}
	if _, ok := r.Get(TypeTeams); !ok {
		t.Fatal("teams")
	}
	if err := r.Register(slackDefinition()); err == nil {
		t.Fatal("dup")
	}
	if SwitchName("slack") != "integrations.slack" {
		t.Fatal(SwitchName("slack"))
	}
}

func TestCheckIfMatchAndETag(t *testing.T) {
	if err := checkIfMatch(`"v2"`, 1); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if err := checkIfMatch(`"v2"`, 2); err != nil {
		t.Fatal(err)
	}
	if formatETag(3) != `"v3"` {
		t.Fatal(formatETag(3))
	}
	v := viewOf(&Instance{ID: "int_x", Config: nil, Secrets: nil}, []string{"password"})
	if v.Config == nil || v.SecretsSet["password"] {
		t.Fatalf("%+v", v)
	}
}
