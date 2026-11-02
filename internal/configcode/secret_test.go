package configcode

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// fakeEnv serves per-organisation settings.env values; keys listed in secret are stored as secrets.
type fakeEnv struct {
	orgs   map[string]map[string]string
	secret map[string][]string
	err    error
	asked  []string
}

func (f *fakeEnv) RuleEnv(_ context.Context, orgID string) (map[string]string, []string, error) {
	f.asked = append(f.asked, orgID)
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.orgs[orgID], f.secret[orgID], nil
}

func twoOrgEnv() *fakeEnv {
	return &fakeEnv{
		orgs: map[string]map[string]string{
			"org_a": {"HOOK": "a-hook-value", "PLAIN": "not-secret"},
			"org_b": {"HOOK": "b-hook-value", "ONLY_B": "b-only-value"},
		},
		secret: map[string][]string{"org_a": {"HOOK"}, "org_b": {"HOOK", "ONLY_B"}},
	}
}

func TestResolveOrgSecretOwnOrgOnly(t *testing.T) {
	env := twoOrgEnv()
	ctxA := withCaller(context.Background(), "org_a")
	ctxB := withCaller(context.Background(), "org_b")

	v, err := ResolveOrgSecret(ctxA, env, "${secret:HOOK}")
	if err != nil || v != "a-hook-value" {
		t.Fatalf("own secret: %q %v", v, err)
	}
	v, err = ResolveOrgSecret(ctxB, env, "${HOOK}")
	if err != nil || v != "b-hook-value" {
		t.Fatalf("shorthand: %q %v", v, err)
	}
	// Cross-tenant: org A can never reach org B's secret.
	_, err = ResolveOrgSecret(ctxA, env, "${secret:ONLY_B}")
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("cross-tenant: %v", err)
	}
	for _, o := range env.asked {
		if o != "org_a" && o != "org_b" {
			t.Fatalf("unexpected org lookup %s", o)
		}
	}
	if env.asked[len(env.asked)-1] != "org_a" {
		t.Fatal("must load the caller's org only")
	}
}

func TestResolveOrgSecretNeverProcessEnvOrFiles(t *testing.T) {
	t.Setenv("EB_AUTH_SECRET", "server-auth-secret-fake")
	t.Setenv("EB_KEK", "server-kek-fake")
	t.Setenv("PASSWD_FILE", "/etc/passwd")
	env := twoOrgEnv()
	ctx := withCaller(context.Background(), "org_a")
	for _, ref := range []string{"${secret:EB_AUTH_SECRET}", "${EB_KEK}", "${secret:PASSWD}", "${secret:EB_CONFIG_VAR_X}"} {
		v, err := ResolveOrgSecret(ctx, env, ref)
		if err == nil || v != "" {
			t.Fatalf("%s resolved to %q", ref, v)
		}
		if errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%s: %v", ref, err)
		}
		name, _ := SecretName(ref)
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error must name the variable: %v", err)
		}
		if strings.Contains(err.Error(), "fake") || strings.Contains(err.Error(), "/etc/passwd") {
			t.Fatalf("error leaks a value: %v", err)
		}
	}
}

func TestResolveOrgSecretRejectsNonSecretAndEdgeCases(t *testing.T) {
	env := twoOrgEnv()
	ctx := withCaller(context.Background(), "org_a")
	// A plain (non-secret) env key is not a secret source.
	if _, err := ResolveOrgSecret(ctx, env, "${secret:PLAIN}"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("plain env: %v", err)
	} else if strings.Contains(err.Error(), "not-secret") {
		t.Fatal("leaked value")
	}
	if v, err := ResolveOrgSecret(ctx, env, "  "); err != nil || v != "" {
		t.Fatal("empty ref")
	}
	if _, err := ResolveOrgSecret(ctx, env, "inline-value"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("inline: %v", err)
	}
	if _, err := ResolveOrgSecret(context.Background(), env, "${secret:HOOK}"); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("no caller: %v", err)
	}
	if _, err := ResolveOrgSecret(ctx, nil, "${secret:HOOK}"); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no source: %v", err)
	}
	env.err = errors.New("boom")
	if _, err := ResolveOrgSecret(ctx, env, "${secret:HOOK}"); err == nil {
		t.Fatal("load error")
	}
}

func TestPlanFailsOnUndefinedSecretWithoutValues(t *testing.T) {
	env := twoOrgEnv()
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_a", Role: tenancy.RoleOwner,
	})
	svc := &Service{SkipFeatureGate: true, Env: env, Webhooks: &fakeWebhooks{}}
	doc := Document{Spec: Spec{WebhookEndpoints: []EndpointSpec{{
		Name: "crm", Kind: "http", URL: "https://crm.example.com/hook", Events: []string{"*"},
		Secret:  "${secret:HOOK}",
		Headers: []webhook.WebhookHeader{{Name: "X-Key", Value: "${secret:HOOK}", Secret: true}},
	}}}}
	plan, err := svc.Plan(ctx, doc, false)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if strings.Contains(mustJSON(t, plan), "a-hook-value") {
		t.Fatal("plan reveals a resolved secret")
	}

	doc.Spec.WebhookEndpoints[0].Headers[0].Value = "${secret:ONLY_B}"
	if _, err := svc.Plan(ctx, doc, false); errs.CodeOf(err) != errs.CodeValidationFailed ||
		!strings.Contains(err.Error(), "ONLY_B") || strings.Contains(err.Error(), "b-only-value") {
		t.Fatalf("header ref: %v", err)
	}
	doc.Spec.WebhookEndpoints[0].Headers = nil
	doc.Spec.WebhookEndpoints[0].Secret = "${secret:EB_AUTH_SECRET}"
	if _, err := svc.Plan(ctx, doc, false); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("endpoint ref: %v", err)
	}
	doc.Spec.WebhookEndpoints[0].Secret = ""
	doc.Spec.Routes = []RouteSpec{{Name: "r", Endpoint: "crm", Filter: "true", Secret: "${secret:MISSING}"}}
	if _, err := svc.Plan(ctx, doc, false); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("route ref: %v", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
