package graph

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestValidationToken_capAndControls(t *testing.T) {
	h := &CallbackHandler{}
	long := strings.Repeat("a", maxValidationToken+1)
	req := httptest.NewRequest(http.MethodPost, "/callbacks/graph?validationToken="+long, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("long: %d", rec.Code)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/callbacks/graph", nil)
	req2.URL.RawQuery = "validationToken=" + url.QueryEscape("ok\x00no")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("nul: %d", rec2.Code)
	}

	req3 := httptest.NewRequest(http.MethodPost, "/callbacks/graph?validationToken=vt-ok", nil)
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != 200 || rec3.Body.String() != "vt-ok" {
		t.Fatalf("%d %q", rec3.Code, rec3.Body.String())
	}
	if ct := rec3.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatal(ct)
	}

	rec4 := httptest.NewRecorder()
	HandleValidation(rec4, strings.Repeat("b", maxValidationToken+1))
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("helper: %d", rec4.Code)
	}
}

func TestTokenURLForTenant_w38(t *testing.T) {
	if got := TokenURLForTenant("tid", ""); !strings.Contains(got, "/tid/") {
		t.Fatal(got)
	}
	if got := TokenURLForTenant("", ""); !strings.Contains(got, "/common/") {
		t.Fatal(got)
	}
	if got := TokenURLForTenant("x", "http://sim/token"); got != "http://sim/token" {
		t.Fatal(got)
	}
}
