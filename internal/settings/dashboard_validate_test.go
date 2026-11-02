package settings

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

// TestValidateDashboardPatch: dashboard.period is an enum (1h|24h|7d|30d); null clears it.
func TestValidateDashboardPatch(t *testing.T) {
	for i, p := range []map[string]any{
		{}, {"dashboard": nil}, {"dashboard": map[string]any{}}, {"dashboard": map[string]any{"period": nil}},
		{"dashboard": map[string]any{"period": "1h"}}, {"dashboard": map[string]any{"period": "24h"}},
		{"dashboard": map[string]any{"period": "7d"}}, {"dashboard": map[string]any{"period": "30d", "other": 1}},
		{"ui": map[string]any{"theme": "dark"}},
	} {
		if err := validateDashboardPatch(p); err != nil {
			t.Fatalf("ok[%d] %v: %v", i, p, err)
		}
	}
	for i, p := range []map[string]any{
		{"dashboard": "24h"}, {"dashboard": map[string]any{"period": "2h"}},
		{"dashboard": map[string]any{"period": 24}}, {"dashboard": map[string]any{"period": "24H"}},
		{"dashboard": map[string]any{"period": ""}},
	} {
		if err := validateDashboardPatch(p); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("bad[%d] %v: %v", i, p, err)
		}
	}
}
