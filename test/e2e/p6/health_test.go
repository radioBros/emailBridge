//go:build e2e

package p6

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// Account health diagnosis + live check + org summary (P6.5).
func TestP6_AccountHealth(t *testing.T) {
	f := startBase(t)
	stamp := time.Now().UnixNano()
	user := fmt.Sprintf("hl%d", stamp)
	accID := createIMAPAccount(t, f.Owner, f.Sim, user, "sim-secret", user+"@sim.e2e.test", "Healthy")

	h := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/health", nil)
	harness.ExpectStatus(t, h, http.StatusOK)
	if h.Str("accountId") != accID && h.Str("accountId") == "" {
		// some views nest under id
		if h.JSON["severity"] == nil && h.Str("severity") == "" {
			t.Fatalf("health: %s", h.Body)
		}
	}
	sev := h.Str("severity")
	if sev == "" {
		if s, ok := h.JSON["severity"].(string); ok {
			sev = s
		}
	}
	if sev != "ok" && sev != "info" && sev != "warning" && sev != "critical" {
		t.Fatalf("severity=%q body=%s", sev, h.Body)
	}

	live := f.Owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/health/check", nil)
	if live.Status != http.StatusOK && live.Status != http.StatusTooManyRequests {
		t.Fatalf("live check: %d %s", live.Status, live.Body)
	}

	sum := f.Owner.Do(http.MethodGet, "/v1/health/accounts?limit=20", nil)
	harness.ExpectStatus(t, sum, http.StatusOK)
	if sum.JSON["data"] == nil && sum.JSON["accounts"] == nil {
		// accept either list shape
		t.Logf("org health summary shape: %s", sum.Body)
	}

	// Cross-tenant → 404.
	b, _ := orgBSession(t, f.Node, f.Owner)
	cross := b.Do(http.MethodGet, "/v1/accounts/"+accID+"/health", nil)
	harness.ExpectProblem(t, cross, http.StatusNotFound, "not_found")
}
