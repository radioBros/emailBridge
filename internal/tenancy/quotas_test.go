package tenancy

import (
	"context"
	"math"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestNormalizeQuotas(t *testing.T) {
	if out, err := NormalizeQuotas(nil); out != nil || err != nil {
		t.Fatalf("nil: %v %v", out, err)
	}
	out, err := NormalizeQuotas(map[string]any{
		QuotaAccounts: float64(50), QuotaSendsPerDay: int32(20000), QuotaSendsPerAccountMinute: nil,
	})
	if err != nil || out[QuotaAccounts] != int64(50) || out[QuotaSendsPerDay] != int64(20000) {
		t.Fatalf("normalised: %v %v", out, err)
	}
	if _, ok := out[QuotaSendsPerAccountMinute]; ok {
		t.Fatal("null must be dropped (unset)")
	}
	if out, err := NormalizeQuotas(map[string]any{QuotaEndpoints: float64(50)}); err != nil || out[QuotaEndpoints] != int64(50) {
		t.Fatalf("endpoints: %v %v", out, err)
	}
	for name, bad := range map[string]map[string]any{
		"unknown":  {"widgets": 5},
		"negative": {QuotaAccounts: -1},
		"fraction": {QuotaAccounts: 1.5},
		"string":   {QuotaSendsPerDay: "10"},
		"bool":     {QuotaSendsPerDay: true},
		"nan":      {QuotaSendsPerDay: math.NaN()},
		"huge":     {QuotaSendsPerDay: float64(1 << 50)},
	} {
		if _, err := NormalizeQuotas(bad); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if len(QuotaKeys()) != 4 || QuotaKeys()[0] != QuotaAccounts {
		t.Fatalf("keys %v", QuotaKeys())
	}
}

func TestParseOrgQuotasLenient(t *testing.T) {
	q := parseOrgQuotas(map[string]any{
		QuotaAccounts: int64(3), QuotaSendsPerDay: "x", QuotaSendsPerAccountMinute: int32(-4), "legacy": 9,
	})
	if q != (OrgQuotas{Accounts: 3}) {
		t.Fatalf("%+v", q)
	}
	if q.Get(QuotaAccounts) != 3 || q.Get(QuotaSendsPerDay) != 0 || q.Get("nope") != 0 {
		t.Fatal("Get")
	}
	q = parseOrgQuotas(map[string]any{QuotaSendsPerDay: 10, QuotaSendsPerAccountMinute: float64(2)})
	if q.Get(QuotaSendsPerDay) != 10 || q.Get(QuotaSendsPerAccountMinute) != 2 {
		t.Fatalf("%+v", q)
	}
	if q, err := LoadOrgQuotas(context.Background(), nil, "org_x"); err != nil || q != (OrgQuotas{}) {
		t.Fatal("nil db = no quotas")
	}
}

func TestEffectiveLimit(t *testing.T) {
	for _, tc := range []struct {
		instance, org, want int
		fromOrg             bool
	}{
		{0, 0, 0, false},   // both unlimited
		{10, 0, 10, false}, // org unset → instance
		{0, 5, 5, true},    // instance unlimited → org
		{10, 5, 5, true},   // org stricter
		{3, 5, 3, false},   // instance stricter
		{5, 5, 5, true},    // tie → organisation
	} {
		got, fromOrg := EffectiveLimit(tc.instance, tc.org)
		if got != tc.want || fromOrg != tc.fromOrg {
			t.Fatalf("EffectiveLimit(%d,%d) = %d,%v want %d,%v", tc.instance, tc.org, got, fromOrg, tc.want, tc.fromOrg)
		}
	}
	e := OrgQuotaExceeded(QuotaSendsPerDay, 7)
	if e.Code != errs.CodeOrganizationQuotaExceeded || e.Msg != "The organisation's sendsPerDay quota (7) is reached." {
		t.Fatalf("%+v", e)
	}
	if p := errs.ProblemFrom(e, "", ""); p.Status != 429 || p.GetHeaders().Get("Retry-After") != "60" {
		t.Fatalf("problem %+v %v", p, p.GetHeaders())
	}
}
