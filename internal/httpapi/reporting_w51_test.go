package httpapi

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestReportableProblemReportedOnce(t *testing.T) {
	var n atomic.Int32
	s, _ := newServer(t, Options{
		OnReportable: func(ctx context.Context, err error) {
			n.Add(1)
			if !errs.Reportable(err) {
				t.Errorf("non-reportable: %v", err)
			}
			if errs.CodeOf(err) != errs.CodeInternalError {
				t.Errorf("code %s", errs.CodeOf(err))
			}
		},
	})
	Register(s, opPatch("/v1/things/{id}"), func(ctx context.Context, in *echoIn) (*echoOut, error) {
		if in.Body.Name == "internal" {
			return nil, errs.New(errs.CodeInternalError, "secret-token-should-not-matter")
		}
		if in.Body.Name == "validation" {
			return nil, errs.New(errs.CodeValidationFailed, "bad")
		}
		return &echoOut{}, nil
	})
	w := do(s, http.MethodPatch, "/v1/things/"+accID, `{"name":"internal"}`, nil)
	if w.Code != 500 {
		t.Fatalf("status %d", w.Code)
	}
	if n.Load() != 1 {
		t.Fatalf("reportable reports=%d", n.Load())
	}
	w = do(s, http.MethodPatch, "/v1/things/"+accID, `{"name":"validation"}`, nil)
	if w.Code != 422 {
		t.Fatalf("validation status %d", w.Code)
	}
	if n.Load() != 1 {
		t.Fatalf("validation must not report: %d", n.Load())
	}
}

func TestWriteProblemReports(t *testing.T) {
	var n atomic.Int32
	s, _ := newServer(t, Options{
		OnReportable: func(context.Context, error) { n.Add(1) },
	})
	s.Router.Get("/v1/raw-err", func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, errs.New(errs.CodeInternalError, "raw"))
	})
	w := do(s, http.MethodGet, "/v1/raw-err", "", nil)
	if w.Code != 500 || n.Load() != 1 {
		t.Fatalf("status=%d reports=%d", w.Code, n.Load())
	}
	s.Router.Get("/v1/raw-nf", func(w http.ResponseWriter, r *http.Request) {
		WriteProblem(w, r, errs.New(errs.CodeNotFound, "gone"))
	})
	w = do(s, http.MethodGet, "/v1/raw-nf", "", nil)
	if w.Code != 404 || n.Load() != 1 {
		t.Fatalf("not_found status=%d reports=%d", w.Code, n.Load())
	}
}

func TestPanicHookStillFires(t *testing.T) {
	var panicked atomic.Bool
	s, _ := newServer(t, Options{
		OnPanic: func(*http.Request, any, []byte) { panicked.Store(true) },
	})
	registerEcho(s)
	w := do(s, http.MethodPatch, "/v1/things/"+accID, `{"name":"panic"}`, nil)
	if w.Code != 500 || !panicked.Load() {
		t.Fatalf("panic status=%d hooked=%v", w.Code, panicked.Load())
	}
}
