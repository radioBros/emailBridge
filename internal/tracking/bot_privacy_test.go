package tracking

import (
	"net/http"
	"net/netip"
	"testing"
)

func TestLikelyBot(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0")
	if LikelyBot(r) {
		t.Fatal("human")
	}
	r.Header.Set("User-Agent", "GoogleImageProxy")
	if !LikelyBot(r) {
		t.Fatal("proxy")
	}
	r2, _ := http.NewRequest(http.MethodHead, "/", nil)
	if !LikelyBot(r2) {
		t.Fatal("head")
	}
}

func TestTruncateIP(t *testing.T) {
	v4, _ := netip.ParseAddr("203.0.113.45")
	if TruncateIP(v4) != "203.0.113.0" {
		t.Fatal(TruncateIP(v4))
	}
	v6, _ := netip.ParseAddr("2001:db8:abcd:0012::1")
	got := TruncateIP(v6)
	if got == "" || got == "2001:db8:abcd:0012::1" {
		t.Fatalf("v6: %s", got)
	}
	if TruncateIP(netip.Addr{}) != "" {
		t.Fatal("invalid")
	}
}
