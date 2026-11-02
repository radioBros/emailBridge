package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Documentation helpers (W70). Every operation added to the OpenAPI document — through Register or through
// OpenAPI().AddOperation for raw handlers — passes documentOperation, which:
//
//   - attaches a standard RFC 9457 example to every problem response, one named example per declared problem
//     code (Problems / ProblemDocs) or, when the operation declares no code for that status, the platform's
//     default code for the status;
//   - adds the request headers the platform middleware handles (EB-Organization, X-Request-Id, EB-Timeout,
//     Idempotency-Key on POST) with descriptions and examples;
//   - fills in the description and example of well-known headers an operation declares itself (If-Match,
//     Last-Event-ID, ETag, Location, Idempotent-Replayed, Retry-After) when the operation left them empty;
//   - documents the X-Request-Id response header on every 2xx response.
//
// Domain packages only declare which problems an operation returns:
//
//	httpapi.Register(srv, huma.Operation{
//		OperationID: "patchAccount", …,
//		Metadata: httpapi.Problems(errs.CodeNotFound, errs.CodeVersionMismatch, errs.CodeExternalIDTaken),
//	}, handler)
//
// and use ProblemDocs when a code needs an operation-specific detail or field errors.

// metaProblems is the huma Operation.Metadata key holding the declared problems ([]ProblemDoc).
const metaProblems = "eb.problems"

// Example identifiers shared by every generated problem example (realistic, pattern-valid).
const (
	ExampleRequestID = "req_01m5c8kym00mr02c50c1r5a3d4"
	ExampleTraceID   = "4bf92f3577b34da6a3ce929d0e0e4736"
	ExampleOrgID     = "org_01m5caqpk0kast63yc13wpy2d9"
)

// ProblemDoc documents one problem an operation can return. Detail and Errors are optional: empty Detail uses
// the standard text for the code.
type ProblemDoc struct {
	Code    errs.Code
	Detail  string
	Errors  []errs.ProblemItem
	Summary string // optional example summary; default: the catalogue title
}

// Problems declares the problem codes an operation can return, for huma.Operation.Metadata. Register adds the
// matching HTTP statuses to the operation's error responses and the document shows one example per code.
func Problems(codes ...errs.Code) map[string]any {
	docs := make([]ProblemDoc, 0, len(codes))
	for _, c := range codes {
		docs = append(docs, ProblemDoc{Code: c})
	}
	return ProblemDocs(docs...)
}

// ProblemDocs is Problems with an operation-specific detail / field errors per code.
func ProblemDocs(docs ...ProblemDoc) map[string]any {
	for _, d := range docs {
		if _, status, _, ok := errs.Lookup(d.Code); !ok || status == 0 {
			panic(fmt.Sprintf("httpapi: problem code %q is unknown or not an HTTP problem (status 0)", d.Code))
		}
	}
	return map[string]any{metaProblems: docs}
}

// WithProblems merges declared problems into an existing Metadata map (nil allowed).
func WithProblems(md map[string]any, codes ...errs.Code) map[string]any {
	if md == nil {
		md = map[string]any{}
	}
	cur, _ := md[metaProblems].([]ProblemDoc)
	md[metaProblems] = append(cur, Problems(codes...)[metaProblems].([]ProblemDoc)...)
	return md
}

func declaredProblems(op *huma.Operation) []ProblemDoc {
	if op == nil || op.Metadata == nil {
		return nil
	}
	docs, _ := op.Metadata[metaProblems].([]ProblemDoc)
	return docs
}

// problemStatuses returns the HTTP statuses of the declared problems.
func problemStatuses(docs []ProblemDoc) []int {
	var out []int
	for _, d := range docs {
		if _, status, _, ok := errs.Lookup(d.Code); ok && status != 0 {
			out = append(out, status)
		}
	}
	return out
}

func mergeStatuses(a []int, b ...int) []int {
	seen := map[int]bool{}
	out := make([]int, 0, len(a)+len(b))
	for _, s := range append(append([]int{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// defaultProblem is the code and detail shown for a problem status that declares no code.
var defaultProblem = map[int]ProblemDoc{
	http.StatusBadRequest:            {Code: errs.CodeMalformedRequest, Detail: "The request body is not valid JSON: unexpected end of input at offset 42."},
	http.StatusUnauthorized:          {Code: errs.CodeUnauthenticated, Detail: "Sign in, or send an API key as `Authorization: Bearer ebk_…`."},
	http.StatusForbidden:             {Code: errs.CodeForbidden, Detail: "Your role in this organisation does not allow this action."},
	http.StatusNotFound:              {Code: errs.CodeNotFound, Detail: "The requested resource does not exist in this organisation."},
	http.StatusConflict:              {Code: errs.CodeConflict, Detail: "The request conflicts with the current state of the resource."},
	http.StatusGone:                  {Code: errs.CodeExportExpired, Detail: "The export download has expired; start a new export."},
	http.StatusPreconditionFailed:    {Code: errs.CodeVersionMismatch, Detail: "The resource version does not match If-Match."},
	http.StatusRequestEntityTooLarge: {Code: errs.CodePayloadTooLarge, Detail: "The request body exceeds the 32 MiB limit."},
	http.StatusUnsupportedMediaType:  {Code: errs.CodeUnsupportedMediaType, Detail: "Content-Type text/plain is not supported; send application/json."},
	http.StatusUnprocessableEntity: {Code: errs.CodeValidationFailed, Detail: "validation failed", Errors: []errs.ProblemItem{
		{Path: "body.name", Code: "invalid", Message: "expected required property name to be present"},
	}},
	http.StatusLocked:              {Code: errs.CodeAccountLocked, Detail: "Too many failed sign-in attempts; try again in 15 minutes."},
	http.StatusTooManyRequests:     {Code: errs.CodeRateLimited, Detail: "Rate limit exceeded; retry later."},
	http.StatusInternalServerError: {Code: errs.CodeInternalError, Detail: "An internal error occurred."},
	http.StatusServiceUnavailable:  {Code: errs.CodeServiceUnavailable, Detail: "A dependency is temporarily unavailable; retry later."},
	http.StatusGatewayTimeout:      {Code: errs.CodeTimeout, Detail: "The request did not finish within the handler timeout."},
}

// codeDetail gives a realistic detail for codes that are often declared without one.
var codeDetail = map[errs.Code]string{
	errs.CodeScopeMissing:          "The API key lacks the scope this operation requires.",
	errs.CodeOrganizationMismatch:  "The EB-Organization header names a different organisation than the API key.",
	errs.CodeFeatureDisabled:       "This feature is switched off for the organisation.",
	errs.CodeSettingLocked:         "The setting is locked by the platform administrator.",
	errs.CodeSettingNotRuntime:     "The setting can only be changed in the environment; restart required.",
	errs.CodeSwitchUnknown:         "Unknown feature switch.",
	errs.CodeIdempotencyKeyReused:  "The Idempotency-Key was already used with a different request.",
	errs.CodeIdempotencyInProgress: "A request with this Idempotency-Key is still being processed.",
	errs.CodeLastOwner:             "The organisation must keep at least one owner.",
	errs.CodeSortNotAllowed:        "Sorting by this field is not allowed.",
	errs.CodeLogRangeTooLarge:      "The requested time range is larger than the allowed maximum.",
	errs.CodeInvalidCredentials:    "The email address or password is wrong.",
	errs.CodeMFARequired:           "A second factor is required to finish signing in.",
	errs.CodeMFAInvalid:            "The verification code is wrong or expired.",
	errs.CodeSessionExpired:        "The session has expired; sign in again.",
	errs.CodeCSRFFailed:            "Cross-site request rejected.",
	errs.CodeMigrationsPending:     "Database migrations are still running; retry shortly.",
}

// ProblemExample builds the RFC 9457 example body for a code, exactly as errs.ProblemFrom renders it.
func ProblemExample(d ProblemDoc) map[string]any {
	detail := d.Detail
	if detail == "" {
		detail = codeDetail[d.Code]
	}
	if detail == "" {
		if def, ok := defaultProblem[statusOf(d.Code)]; ok && def.Code == d.Code {
			detail = def.Detail
		}
	}
	if detail == "" {
		_, _, title, _ := errs.Lookup(d.Code)
		detail = title + "."
	}
	p := errs.ProblemFrom(errs.New(d.Code, detail), ExampleRequestID, ExampleTraceID)
	p.Errors = d.Errors
	if p.Code == errs.CodeValidationFailed && len(p.Errors) == 0 && d.Detail == "" {
		p.Errors = defaultProblem[http.StatusUnprocessableEntity].Errors
		p.Detail = defaultProblem[http.StatusUnprocessableEntity].Detail
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

func statusOf(c errs.Code) int {
	_, status, _, _ := errs.Lookup(c)
	return status
}

// documentOperation is the OpenAPI OnAddOperation hook (see the package comment above).
func documentOperation(oapi *huma.OpenAPI, op *huma.Operation) {
	if op == nil {
		return
	}
	addCommonHeaders(op)
	documentKnownHeaders(op)
	documentProblems(oapi, op)
	documentSuccessHeaders(op)
}

func documentProblems(oapi *huma.OpenAPI, op *huma.Operation) {
	declared := declaredProblems(op)
	byStatus := map[int][]ProblemDoc{}
	for _, d := range declared {
		s := statusOf(d.Code)
		byStatus[s] = append(byStatus[s], d)
	}
	if op.Responses == nil {
		op.Responses = map[string]*huma.Response{}
	}
	// Declared codes whose status has no response yet (raw operations documented with AddOperation).
	for s := range byStatus {
		key := strconv.Itoa(s)
		if op.Responses[key] == nil {
			op.Responses[key] = &huma.Response{Description: http.StatusText(s)}
		}
	}
	for key, resp := range op.Responses {
		status, err := strconv.Atoi(key)
		if err != nil || status < 400 || resp == nil || resp.Ref != "" {
			continue
		}
		if len(resp.Content) > 0 && resp.Content["application/problem+json"] == nil {
			continue // a documented non-problem error body (e.g. /readyz 503)
		}
		if resp.Content == nil {
			resp.Content = map[string]*huma.MediaType{}
		}
		mt := resp.Content["application/problem+json"]
		if mt == nil {
			mt = &huma.MediaType{Schema: problemSchema(oapi)}
			resp.Content["application/problem+json"] = mt
		}
		if mt.Schema == nil {
			mt.Schema = problemSchema(oapi)
		}
		if mt.Example != nil || len(mt.Examples) > 0 {
			continue // an operation-specific example wins
		}
		docs := byStatus[status]
		if len(docs) == 0 {
			def, ok := defaultProblem[status]
			if !ok {
				continue // the lint reports it: declare the codes with Problems
			}
			docs = []ProblemDoc{def}
		}
		mt.Examples = map[string]*huma.Example{}
		for _, d := range docs {
			_, _, title, _ := errs.Lookup(d.Code)
			summary := d.Summary
			if summary == "" {
				summary = title
			}
			name := string(d.Code)
			if _, dup := mt.Examples[name]; dup {
				name = fmt.Sprintf("%s_%d", d.Code, len(mt.Examples)+1)
			}
			mt.Examples[name] = &huma.Example{Summary: summary, Value: ProblemExample(d)}
		}
		if resp.Description == "" || resp.Description == http.StatusText(status) {
			resp.Description = problemResponseDescription(status, docs)
		}
	}
}

func problemResponseDescription(status int, docs []ProblemDoc) string {
	codes := make([]string, 0, len(docs))
	for _, d := range docs {
		codes = append(codes, "`"+string(d.Code)+"`")
	}
	sort.Strings(codes)
	return http.StatusText(status) + " — problem code " + strings.Join(codes, ", ") + " (RFC 9457 `application/problem+json`)."
}

func problemSchema(oapi *huma.OpenAPI) *huma.Schema {
	if oapi == nil || oapi.Components == nil || oapi.Components.Schemas == nil {
		return nil
	}
	return oapi.Components.Schemas.Schema(reflect.TypeFor[errs.Problem](), true, "Error")
}

// Standard header documentation, used for headers an operation declares without description / example.
type headerDoc struct {
	description string
	example     any
}

var requestHeaderDocs = map[string]headerDoc{
	"if-match": {"Optimistic concurrency: the `ETag` returned by the last read of this resource. When the resource changed since, the request fails with `412 version_mismatch`. Omit to skip the check.",
		`"v3"`},
	"if-none-match": {"The `ETag` of the copy the client already has; the server answers `304 Not Modified` when it is still current.",
		`"v3"`},
	"last-event-id": {"Resume a server-sent event stream after this event ID (sent automatically by `EventSource` on reconnect).",
		"evt_01m5ccvej0dqefkn0xhkcq2q6z"},
	"idempotency-key": {"1–255 printable ASCII. Same key + same request replays the stored response with `Idempotent-Replayed: true`; same key + different request → `409 idempotency_key_reused`. Keys are kept for 24 hours.",
		"9f1c2d7e-4b3a-4c8e-9a51-2f6d0b7c3e18"},
}

var responseHeaderDocs = map[string]headerDoc{
	"etag":                {"Version of the returned resource; send it back as `If-Match` on the next write.", `"v3"`},
	"location":            {"URL of the created resource.", "/v1/organizations/" + ExampleOrgID},
	"idempotent-replayed": {"`true` when this response is a stored replay of an earlier request with the same `Idempotency-Key`.", "true"},
	"retry-after":         {"Seconds to wait before retrying.", "900"},
	"x-request-id":        {"Request ID, echoed from the request or generated (`req_<ulid>`); quote it when reporting a problem.", ExampleRequestID},
	"content-disposition": {"Download file name.", `attachment; filename="export.ndjson"`},
	"cache-control":       {"Caching policy of the response.", "no-store"},
}

func documentKnownHeaders(op *huma.Operation) {
	for _, p := range op.Parameters {
		if p == nil || p.In != "header" || p.Ref != "" {
			continue
		}
		if d, ok := requestHeaderDocs[strings.ToLower(p.Name)]; ok {
			fillParam(p, d)
		}
	}
	for _, resp := range op.Responses {
		if resp == nil {
			continue
		}
		for name, h := range resp.Headers {
			if h == nil || h.Ref != "" {
				continue
			}
			if h.Description == "" && h.Schema != nil && h.Schema.Description != "" {
				h.Description = h.Schema.Description // huma puts a response header's doc tag on its schema
			}
			if d, ok := responseHeaderDocs[strings.ToLower(name)]; ok {
				fillParam(h, d)
			}
		}
	}
}

func fillParam(p *huma.Param, d headerDoc) {
	if p.Description == "" && p.Schema != nil && p.Schema.Description != "" {
		p.Description = p.Schema.Description // huma puts a response header's doc tag on its schema
	}
	if p.Description == "" {
		p.Description = d.description
	}
	if p.Example == nil && len(p.Examples) == 0 && (p.Schema == nil || len(p.Schema.Examples) == 0) {
		p.Example = d.example
	}
}

// documentSuccessHeaders documents X-Request-Id on every 2xx response.
func documentSuccessHeaders(op *huma.Operation) {
	for key, resp := range op.Responses {
		status, err := strconv.Atoi(key)
		if err != nil || status < 200 || status >= 300 || resp == nil || resp.Ref != "" {
			continue
		}
		// Copy first: documentation-only operations may share one headers map between responses.
		headers := make(map[string]*huma.Param, len(resp.Headers)+2)
		for k, v := range resp.Headers {
			headers[k] = v
		}
		resp.Headers = headers
		if _, ok := resp.Headers["X-Request-Id"]; !ok {
			d := responseHeaderDocs["x-request-id"]
			resp.Headers["X-Request-Id"] = &huma.Param{Description: d.description, Example: d.example,
				Schema: &huma.Schema{Type: "string"}}
		}
		if idempotent(op) {
			if _, ok := resp.Headers["Idempotent-Replayed"]; !ok {
				d := responseHeaderDocs["idempotent-replayed"]
				resp.Headers["Idempotent-Replayed"] = &huma.Param{Description: d.description, Example: d.example,
					Schema: &huma.Schema{Type: "string", Enum: []any{"true"}}}
			}
		}
	}
}

// addCommonHeaders declares the request headers the platform middleware handles for every operation
// (S2 §2), so the generated document shows them to clients. Headers an operation declares itself win.
func addCommonHeaders(op *huma.Operation) {
	if !strings.HasPrefix(op.Path, "/v1/") {
		return // probes and other root paths take none of the API headers
	}
	have := map[string]bool{}
	for _, p := range op.Parameters {
		if p != nil && p.In == "header" {
			have[strings.ToLower(p.Name)] = true
		}
	}
	add := func(p *huma.Param) {
		if !have[strings.ToLower(p.Name)] {
			op.Parameters = append(op.Parameters, p)
		}
	}
	one, maxKey := 1, 255
	minTimeout := 1.0
	add(&huma.Param{Name: "EB-Organization", In: "header",
		Description: "Active organisation for session callers (`org_…`). Default: the user's last used organisation. An API key sending a different organisation gets `403 organization_mismatch`.",
		Schema:      &huma.Schema{Type: "string", Pattern: "^org_[0-9a-z]{26}$"}, Example: ExampleOrgID})
	add(&huma.Param{Name: "X-Request-Id", In: "header",
		Description: "Accepted if `[A-Za-z0-9._-]{8,64}`; otherwise generated as `req_<ulid>`. Echoed in the response.",
		Schema:      &huma.Schema{Type: "string", Pattern: "^[A-Za-z0-9._-]{8,64}$"}, Example: ExampleRequestID})
	add(&huma.Param{Name: "EB-Timeout", In: "header",
		Description: "Handler timeout in milliseconds, within `EB_HTTP_REQUEST_TIMEOUT_MAX`.",
		Schema:      &huma.Schema{Type: "integer", Minimum: &minTimeout}, Example: 30000})
	if idempotent(op) {
		d := requestHeaderDocs["idempotency-key"]
		add(&huma.Param{Name: "Idempotency-Key", In: "header",
			Description: d.description,
			Schema:      &huma.Schema{Type: "string", MinLength: &one, MaxLength: &maxKey}, Example: d.example})
	}
}

// idempotent reports whether the Idempotency-Key middleware applies: POST under /v1/ except the sign-in
// flows (/v1/auth/*, exempt in internal/idempotency so a replay can never hand out a session).
func idempotent(op *huma.Operation) bool {
	return strings.EqualFold(op.Method, http.MethodPost) && strings.HasPrefix(op.Path, "/v1/") &&
		!strings.HasPrefix(op.Path, "/v1/auth/")
}
