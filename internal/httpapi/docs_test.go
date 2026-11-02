package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
)

type docsIn struct {
	ID      string `path:"id" doc:"Thing ID." example:"thg_01m5c8kym00mr02c50c1r5a3d4"`
	IfMatch string `header:"If-Match"`
	Body    struct {
		Name string `json:"name" doc:"Display name." example:"Billing"`
	}
}

type docsOut struct {
	ETag string `header:"ETag"`
	Body struct {
		Name string `json:"name" doc:"Display name." example:"Billing"`
	}
}

func newDocsServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(Options{Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDocsProblemExamplesByCodeAndDefault(t *testing.T) {
	s := newDocsServer(t)
	Register(s, huma.Operation{
		OperationID: "patchThing", Method: http.MethodPatch, Path: "/v1/things/{id}", Summary: "Patch thing",
		Metadata: ProblemDocs(
			ProblemDoc{Code: errs.CodeVersionMismatch},
			ProblemDoc{Code: errs.CodeNotFound, Detail: "Thing thg_01m5c8kym00mr02c50c1r5a3d4 not found."},
			ProblemDoc{Code: errs.CodeLastOwner},
			ProblemDoc{Code: errs.CodeConflict},
		),
	}, func(context.Context, *docsIn) (*docsOut, error) { return &docsOut{}, nil })

	op := s.API.OpenAPI().Paths["/v1/things/{id}"].Patch
	if op == nil {
		t.Fatal("operation missing")
	}
	// 412 and 404 are not in the PATCH defaults: declaring the codes adds the responses.
	for _, st := range []string{"404", "412", "409", "401", "422", "500"} {
		r := op.Responses[st]
		if r == nil {
			t.Fatalf("response %s missing", st)
		}
		mt := r.Content["application/problem+json"]
		if mt == nil || len(mt.Examples) == 0 {
			t.Fatalf("response %s has no problem example", st)
		}
	}
	nf := op.Responses["404"].Content["application/problem+json"].Examples["not_found"]
	if nf == nil {
		t.Fatal("named example not_found missing")
	}
	body, _ := nf.Value.(map[string]any)
	if body["detail"] != "Thing thg_01m5c8kym00mr02c50c1r5a3d4 not found." || body["status"] != float64(404) ||
		body["type"] != "https://emailbridge.dev/problems/not_found" || body["kind"] != "not_found" || body["requestId"] != ExampleRequestID {
		t.Fatalf("not_found example = %#v", body)
	}
	conflict := op.Responses["409"].Content["application/problem+json"].Examples
	if conflict["last_owner"] == nil || conflict["conflict"] == nil {
		t.Fatalf("409 examples = %v", conflict)
	}
	v := op.Responses["422"].Content["application/problem+json"].Examples["validation_failed"]
	if v == nil {
		t.Fatal("default 422 example missing")
	}
	if items, _ := v.Value.(map[string]any)["errors"].([]any); len(items) != 1 {
		t.Fatalf("422 example errors = %v", v.Value)
	}
	if !strings.Contains(op.Responses["412"].Description, "version_mismatch") {
		t.Fatalf("412 description = %q", op.Responses["412"].Description)
	}

	// Headers: common ones carry examples; known ones are filled in.
	var ifMatch, org *huma.Param
	for _, p := range op.Parameters {
		switch p.Name {
		case "If-Match":
			ifMatch = p
		case "EB-Organization":
			org = p
		}
	}
	if ifMatch == nil || ifMatch.Description == "" || ifMatch.Example == nil {
		t.Fatalf("If-Match not documented: %+v", ifMatch)
	}
	if org == nil || org.Example != ExampleOrgID {
		t.Fatalf("EB-Organization example: %+v", org)
	}
	ok := op.Responses["200"]
	if ok.Headers["ETag"] == nil || ok.Headers["ETag"].Description == "" || ok.Headers["X-Request-Id"] == nil {
		t.Fatalf("200 headers = %v", ok.Headers)
	}
}

func TestDocsOperationExampleWins(t *testing.T) {
	s := newDocsServer(t)
	own := map[string]any{"code": "custom"}
	op := &huma.Operation{
		OperationID: "rawThing", Method: http.MethodPost, Path: "/v1/raw", Summary: "Raw",
		Responses: map[string]*huma.Response{
			"400": {Description: "Bad", Content: map[string]*huma.MediaType{"application/problem+json": {Example: own}}},
		},
		Metadata: Problems(errs.CodeInvalidCredentials, errs.CodeAccountLocked),
	}
	s.Document(op)
	got := s.API.OpenAPI().Paths["/v1/raw"].Post
	if got.Responses["400"].Content["application/problem+json"].Examples != nil {
		t.Fatal("operation example was overwritten")
	}
	if got.Responses["401"] == nil || got.Responses["423"] == nil {
		t.Fatalf("declared statuses missing: %v", got.Responses)
	}
	if got.Responses["423"].Content["application/problem+json"].Examples["account_locked"] == nil {
		t.Fatal("423 example missing")
	}
	var idem bool
	for _, p := range got.Parameters {
		idem = idem || p.Name == "Idempotency-Key"
	}
	if !idem {
		t.Fatal("POST without Idempotency-Key header doc")
	}
}

func TestDocsProbesAndAttemptOnlyCode(t *testing.T) {
	s := newDocsServer(t)
	ready := s.API.OpenAPI().Paths["/readyz"]
	if ready == nil || ready.Get == nil || ready.Get.Responses["503"].Content["application/problem+json"] != nil {
		t.Fatal("/readyz 503 must stay a JSON status body")
	}
	if s.API.OpenAPI().Paths["/healthz"] == nil {
		t.Fatal("/healthz not documented")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("attempt-only code accepted")
		}
	}()
	_ = Problems(errs.CodeSMTPTemporary)
}

func TestWithProblemsMerges(t *testing.T) {
	md := WithProblems(nil, errs.CodeNotFound)
	md = WithProblems(md, errs.CodeConflict)
	if got := declaredProblems(&huma.Operation{Metadata: md}); len(got) != 2 {
		t.Fatalf("declared = %v", got)
	}
	if got := mergeStatuses([]int{401, 404}, 404, 409); len(got) != 3 {
		t.Fatalf("merge = %v", got)
	}
}

func TestDocsSharedHeadersAndAuthExemption(t *testing.T) {
	s := newDocsServer(t)
	shared := map[string]*huma.Param{"Set-Cookie": {Description: "Session cookie.", Schema: &huma.Schema{Type: "string"}, Example: "eb_session=x"}}
	s.Document(&huma.Operation{OperationID: "authPost", Method: http.MethodPost, Path: "/v1/auth/thing", Summary: "Auth post",
		Responses: map[string]*huma.Response{"200": {Description: "OK", Headers: shared}}})
	s.Document(&huma.Operation{OperationID: "plainPost", Method: http.MethodPost, Path: "/v1/thing", Summary: "Post",
		Responses: map[string]*huma.Response{"200": {Description: "OK", Headers: shared}}})
	s.Document(&huma.Operation{OperationID: "plainGet", Method: http.MethodGet, Path: "/v1/thing", Summary: "Get",
		Responses: map[string]*huma.Response{"200": {Description: "OK", Headers: shared}}})
	paths := s.API.OpenAPI().Paths
	if _, ok := paths["/v1/thing"].Get.Responses["200"].Headers["Idempotent-Replayed"]; ok {
		t.Fatal("GET picked up Idempotent-Replayed through a shared headers map")
	}
	if _, ok := paths["/v1/thing"].Post.Responses["200"].Headers["Idempotent-Replayed"]; !ok {
		t.Fatal("POST lacks Idempotent-Replayed")
	}
	auth := paths["/v1/auth/thing"].Post
	if _, ok := auth.Responses["200"].Headers["Idempotent-Replayed"]; ok {
		t.Fatal("auth route documents Idempotent-Replayed")
	}
	for _, p := range auth.Parameters {
		if p.Name == "Idempotency-Key" {
			t.Fatal("auth route documents Idempotency-Key")
		}
	}
	if len(shared) != 1 {
		t.Fatalf("shared map mutated: %v", shared)
	}
}
