package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/configcode"
)

func TestConfigCmdExportPlanApply(t *testing.T) {
	var sawPlan, sawApply bool
	plan := configcode.Plan{
		PlanHash: "sha256:abc",
		Actions: []configcode.Action{
			{Op: "create", Kind: "webhookEndpoint", Name: "billing",
				Changes: map[string]any{"secret": "(changed)", "url": map[string]any{"after": "https://x"}}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("EB-Organization") != "org_01h00000000000000000000000" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/config/export":
			_ = json.NewEncoder(w).Encode(configcode.Document{
				APIVersion: configcode.APIVersion, Kind: configcode.KindOrg,
				Metadata: configcode.Metadata{Organization: "acme"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/config/plan":
			sawPlan = true
			_ = json.NewEncoder(w).Encode(plan)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/config/apply":
			sawApply = true
			var req configcode.ApplyRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.PlanHash != plan.PlanHash {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"code":"config_drift"}`))
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{"job": map[string]any{"id": "job_1"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	file := filepath.Join(dir, "cfg.yaml")
	yaml := []byte(`apiVersion: emailbridge.dev/v1
kind: OrganizationConfig
metadata:
  organization: acme
spec:
  webhookEndpoints:
    - name: billing
      url: https://billing.example.com/hooks
      events: [message.received]
      secret: ${secret:BILLING_WEBHOOK_SECRET}
`)
	if err := os.WriteFile(file, yaml, 0o600); err != nil {
		t.Fatal(err)
	}

	org := "org_01h00000000000000000000000"
	var out, errb bytes.Buffer
	if code := configCmd([]string{"export", "--url", srv.URL, "--org", org, "--token", "tok", "--output", "json"}, &out, &errb); code != 0 {
		t.Fatalf("export: %d %s", code, errb.String())
	}
	if !strings.Contains(out.String(), `"organization":"acme"`) && !strings.Contains(out.String(), `"organization": "acme"`) {
		t.Fatalf("export body=%s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := configCmd([]string{"plan", "-f", file, "--url", srv.URL, "--org", org, "--token", "tok"}, &out, &errb); code != 0 {
		t.Fatalf("plan: %d %s", code, errb.String())
	}
	if !sawPlan || !strings.Contains(out.String(), "sha256:abc") {
		t.Fatalf("plan out=%q", out.String())
	}
	if strings.Contains(out.String(), "whsec_") || strings.Contains(strings.ToLower(out.String()), "secret_value") {
		t.Fatal("plan must not print secret values")
	}
	if !strings.Contains(out.String(), "(changed)") {
		t.Fatalf("expected redacted secret marker: %s", out.String())
	}

	out.Reset()
	errb.Reset()
	if code := configCmd([]string{"apply", "-f", file, "--url", srv.URL, "--org", org, "--token", "tok"}, &out, &errb); code != 0 {
		t.Fatalf("apply: %d %s", code, errb.String())
	}
	if !sawApply || !strings.Contains(out.String(), "job_1") {
		t.Fatalf("apply out=%q", out.String())
	}
}

func TestConfigCmdUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := configCmd(nil, &out, &errb); code != 2 {
		t.Fatalf("code=%d", code)
	}
	if code := configCmd([]string{"plan", "--url", "http://x", "--org", "o", "--token", "t"}, &out, &errb); code != 2 {
		t.Fatalf("missing file: %d", code)
	}
}

func TestRedactBody(t *testing.T) {
	if redactBody([]byte(`{"token":"ebk_abc"}`)) != "(response redacted)" {
		t.Fatal("expected redaction")
	}
}
