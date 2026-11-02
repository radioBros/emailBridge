package errs_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestHumaValidationProblem(t *testing.T) {
	errs.InstallHuma()

	mux := chi.NewMux()
	cfg := huma.DefaultConfig("EmailBridge", "0.0.1")
	api := humachi.New(mux, cfg)

	type Input struct {
		ID string `path:"id" minLength:"3"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "get-thing",
		Method:      http.MethodGet,
		Path:        "/v1/things/{id}",
	}, func(_ context.Context, _ *Input) (*struct{}, error) {
		return &struct{}{}, nil
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/things/ab", nil)
	req.Header.Set("Accept", "application/problem+json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/problem+json") {
		t.Fatalf("content-type %q", ct)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["code"] != "validation_failed" {
		t.Fatalf("code %v", body["code"])
	}
	if body["kind"] != "validation" {
		t.Fatalf("kind %v", body["kind"])
	}
	errsArr, ok := body["errors"].([]any)
	if !ok || len(errsArr) == 0 {
		t.Fatalf("errors %v", body["errors"])
	}
	first, _ := errsArr[0].(map[string]any)
	if first["code"] != "invalid" {
		t.Fatalf("item %v", first)
	}
	path, _ := first["path"].(string)
	if path == "" || (!strings.Contains(path, "id") && !strings.Contains(path, "path")) {
		t.Fatalf("path %q", path)
	}

	// OpenAPI must include the Problem schema with a code property (from NewError).
	oa := api.OpenAPI()
	raw, err := oa.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, `"Problem"`) && !strings.Contains(s, "Problem") {
		t.Fatalf("OpenAPI missing Problem schema: %s", truncate(s, 800))
	}
	if !strings.Contains(s, `"code"`) {
		t.Fatalf("OpenAPI missing code property: %s", truncate(s, 800))
	}
	_ = io.Discard
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
