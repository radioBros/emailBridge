package autoconfig

import "testing"

func TestPECPresets(t *testing.T) {
	r := lookupBuiltin("legalmail.it")
	if r == nil || !r.PEC || r.PECDefaults == nil || r.PECDefaults.SendPolicy != "atMostOnce" || !r.PECDefaults.Switches["pec.enabled"] {
		t.Fatalf("legalmail preset: %+v", r)
	}
	if g := lookupBuiltin("gmail.com"); g == nil || g.PEC || g.PECDefaults != nil {
		t.Fatalf("ordinary provider marked PEC: %+v", g)
	}
	if mx := lookupFromMX("mx.postecert.it"); mx == nil || !mx.PEC {
		t.Fatalf("MX path keeps the PEC flag: %+v", mx)
	}
	for _, tc := range []struct {
		domain, host string
		want         bool
	}{
		{"pec.it", "", true}, {"azienda.it", "imaps.pec.aruba.it", true}, {"aruba.it", "imaps.aruba.it", false}, {"", "", false},
	} {
		if got := IsPEC(tc.domain, tc.host); got != tc.want {
			t.Errorf("IsPEC(%q, %q) = %v", tc.domain, tc.host, got)
		}
	}
}
