package observability_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Required alert names from S12 §9.
var requiredAlerts = []string{
	"EBAuthFailuresRising",
	"EBDeadLetters",
	"EBEndpointFailing",
	"EBLeaseChurn",
	"EBRelayLag",
	"EBLogLoss",
	"EBLogDiskNearCap",
	"EBSyncCapacity",
	"EBMigrationsPending",
	"EBInsecureTLSAccounts",
	"EBRuleFailing",
}

// Metric names every alert expr may reference (S12 §9 + supporting gauges).
var knownMetrics = map[string]bool{
	"eb_errors_total":           true,
	"eb_log_records_total":      true,
	"eb_log_dropped_total":      true,
	"eb_log_disk_bytes":         true,
	"eb_log_disk_cap_bytes":     true,
	"eb_account_state":          true,
	"eb_auth_failures_total":    true,
	"eb_deadletters_total":      true,
	"eb_endpoint_breaker_open":  true,
	"eb_lease_changes_total":    true,
	"eb_relay_lag_seconds":      true,
	"eb_sync_accounts":          true,
	"eb_sync_unplaced_accounts": true,
	"eb_sync_max_accounts":      true,
	"eb_stream_pending":         true,
	"eb_stream_ack_pending":     true,
	"eb_stream_redeliveries":    true,
	"eb_migrations_pending":     true,
	"eb_insecure_tls_accounts":  true,
	"eb_rule_errors_recent":     true,
}

type promRulesFile struct {
	Groups []struct {
		Name  string `yaml:"name"`
		Rules []struct {
			Alert       string            `yaml:"alert"`
			Expr        string            `yaml:"expr"`
			For         string            `yaml:"for"`
			Labels      map[string]string `yaml:"labels"`
			Annotations map[string]string `yaml:"annotations"`
		} `yaml:"rules"`
	} `yaml:"groups"`
}

func TestAlertsYAML(t *testing.T) {
	path := alertsPath(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file promRulesFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(file.Groups) == 0 {
		t.Fatal("no groups")
	}
	seen := map[string]bool{}
	for _, g := range file.Groups {
		for _, r := range g.Rules {
			if r.Alert == "" {
				t.Fatal("rule missing alert name")
			}
			if r.Expr == "" {
				t.Fatalf("%s: empty expr", r.Alert)
			}
			if r.For == "" {
				t.Fatalf("%s: missing for:", r.Alert)
			}
			if r.Labels["severity"] == "" {
				t.Fatalf("%s: missing severity", r.Alert)
			}
			if r.Annotations["summary"] == "" || r.Annotations["description"] == "" {
				t.Fatalf("%s: missing summary/description", r.Alert)
			}
			seen[r.Alert] = true
			for _, metric := range metricNamesInExpr(r.Expr) {
				if !knownMetrics[metric] {
					t.Fatalf("%s: expr references unknown metric %q", r.Alert, metric)
				}
			}
		}
	}
	for _, name := range requiredAlerts {
		if !seen[name] {
			t.Fatalf("missing alert %s", name)
		}
	}
}

func alertsPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	// internal/observability -> repo root
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	return filepath.Join(root, "deploy", "observability", "alerts.yml")
}

func metricNamesInExpr(expr string) []string {
	var out []string
	seen := map[string]bool{}
	for _, tok := range strings.FieldsFunc(expr, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_'
	}) {
		if strings.HasPrefix(tok, "eb_") && !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out
}
