package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// An output struct whose Status field was left at zero must not panic the request (it answered 500 for
// GET mailbox / draft before the final pass).
func TestStatusWriterZeroCodeIsOK(t *testing.T) {
	rec := httptest.NewRecorder()
	w := &statusWriter{ResponseWriter: rec}
	w.WriteHeader(0)
	if rec.Code != http.StatusOK || w.status != http.StatusOK {
		t.Fatalf("code = %d / %d, want 200", rec.Code, w.status)
	}
}
