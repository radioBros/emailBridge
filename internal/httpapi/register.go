package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
)

// Register adds a huma operation with EmailBridge's defaults: body limit from EB_HTTP_MAX_BODY, the
// problem responses every operation can return plus the statuses of the problems declared with Problems /
// ProblemDocs in op.Metadata, and the phase/scope extensions used by the contract tests (S2 §1). Domain
// packages call it from their Register(api) functions instead of huma.Register. The documentation hook
// (docs.go) adds problem examples and the common headers when huma adds the operation to the document.
func Register[I, O any](s *Server, op huma.Operation, h func(context.Context, *I) (*O, error)) {
	if op.MaxBodyBytes == 0 {
		op.MaxBodyBytes = s.o.MaxBody
	}
	if op.Errors == nil {
		op.Errors = defaultErrors(op.Method)
	}
	if extra := problemStatuses(declaredProblems(&op)); len(extra) > 0 {
		op.Errors = mergeStatuses(op.Errors, extra...)
	}
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	huma.Register(s.API, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := h(ctx, in)
		if err != nil {
			var se huma.StatusError
			if errors.As(err, &se) {
				return nil, err
			}
			s.reportIfNeeded(ctx, err)
			ids := logging.IDsFrom(ctx)
			return nil, errs.ProblemFrom(err, ids.RequestID, ids.TraceID)
		}
		return out, nil
	})
}

// Document adds a documentation-only operation for a raw handler mounted on Router (Limen, SSE, health
// probes …). It passes the same documentation hook as Register: common headers, and problem responses with
// examples for op.Errors and the problems declared with Problems / ProblemDocs in op.Metadata.
func (s *Server) Document(op *huma.Operation) {
	if op.Responses == nil {
		op.Responses = map[string]*huma.Response{}
	}
	for _, st := range mergeStatuses(op.Errors, problemStatuses(declaredProblems(op))...) {
		key := strconv.Itoa(st)
		if op.Responses[key] == nil {
			op.Responses[key] = &huma.Response{Description: http.StatusText(st)}
		}
	}
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	s.API.OpenAPI().AddOperation(op)
}

// Phase marks the release that introduced an operation. It is kept for the call sites' readability and
// adds nothing to the published document.
func Phase(_ string) map[string]any { return map[string]any{} }

// WithScope adds x-eb-scope to an extensions map.
func WithScope(ext map[string]any, scope string) map[string]any {
	if ext == nil {
		ext = map[string]any{}
	}
	ext["x-eb-scope"] = scope
	return ext
}

func defaultErrors(method string) []int {
	codes := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusInternalServerError}
	switch strings.ToUpper(method) {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		codes = append(codes, http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusConflict, http.StatusRequestEntityTooLarge)
	case http.MethodDelete:
		codes = append(codes, http.StatusNotFound, http.StatusConflict, http.StatusPreconditionFailed)
	default:
		codes = append(codes, http.StatusNotFound)
	}
	return codes
}
