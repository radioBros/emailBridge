package errs

import (
	"errors"
	"net/http"
)

const problemTypeBase = "https://emailbridge.dev/problems/"

// Problem is the RFC 9457 problem details body (S2 §3).
type Problem struct {
	Type      string        `json:"type" doc:"URI of the problem type: https://emailbridge.dev/problems/<code>." example:"https://emailbridge.dev/problems/not_found"`
	Title     string        `json:"title" doc:"Short, stable summary of the problem type." example:"Not found"`
	Status    int           `json:"status" doc:"HTTP status code." example:"404"`
	Detail    string        `json:"detail,omitempty" doc:"Explanation specific to this occurrence (safe to show; never contains secrets)." example:"The requested resource does not exist in this organisation."`
	Instance  string        `json:"instance,omitempty" doc:"URI of this occurrence, when set." example:"/v1/accounts/acc_01m5cvnpb031g57sqct6mzhq94"`
	Code      Code          `json:"code" doc:"Stable machine-readable problem code (see the problem catalogue)." example:"not_found"`
	Kind      Kind          `json:"kind" doc:"Error kind: validation, auth, forbidden, not_found, conflict, rate_limited, temporary, permanent, config, outcome_unknown or internal." example:"not_found"`
	TraceID   string        `json:"traceId,omitempty" doc:"W3C trace ID of the request." example:"4bf92f3577b34da6a3ce929d0e0e4736"`
	RequestID string        `json:"requestId,omitempty" doc:"Request ID (X-Request-Id); quote it when reporting a problem." example:"req_01m5c8kym00mr02c50c1r5a3d4"`
	Errors    []ProblemItem `json:"errors,omitempty" doc:"Field-level validation failures (validation_failed)."`

	// headers are sent with the problem (Retry-After, RateLimit-*); never part of the body.
	headers http.Header
}

// ProblemItem is one field-level validation failure.
type ProblemItem struct {
	Path    string `json:"path" doc:"Location of the invalid value (body.<field>, query.<name> …)." example:"body.name"`
	Code    string `json:"code" doc:"Failure code." example:"invalid"`
	Message string `json:"message" doc:"Human-readable explanation." example:"expected required property name to be present"`
}

// Error implements error and huma.StatusError.
func (p *Problem) Error() string {
	if p == nil {
		return ""
	}
	if p.Detail != "" {
		return p.Detail
	}
	if p.Title != "" {
		return p.Title
	}
	return string(p.Code)
}

// GetStatus implements huma.StatusError.
func (p *Problem) GetStatus() int {
	if p == nil {
		return http.StatusInternalServerError
	}
	return p.Status
}

// ContentType returns application/problem+json for any accept value.
// This satisfies huma's content-type negotiation for custom status errors.
func (p *Problem) ContentType(string) string {
	return "application/problem+json"
}

// ProblemFrom maps err to a Problem suitable for an HTTP response.
// Unknown / internal errors never leak the original error text.
func ProblemFrom(err error, requestID, traceID string) *Problem {
	if err == nil {
		return problemOf(CodeInternalError, "An internal error occurred.", requestID, traceID, nil)
	}
	var e *E
	if errors.As(err, &e) {
		code := e.Code
		if code == "" {
			code = CodeInternalError
		}
		info, ok := catalog[code]
		status := 0
		title := ""
		kind := e.Kind
		if ok {
			status = info.Status
			title = info.Title
			if kind == "" {
				kind = info.Kind
			}
		}
		if status == 0 {
			if kind == "" {
				kind = KindInternal
			}
			status = kindDefaultStatus(kind)
		}
		if title == "" {
			title = http.StatusText(status)
		}
		detail := e.Msg
		if detail == "" {
			detail = title
		}
		if code == CodeInternalError || kind == KindInternal {
			if e.Msg == "" {
				detail = "An internal error occurred."
			}
		}
		return &Problem{
			Type:      problemTypeBase + string(code),
			Title:     title,
			Status:    status,
			Detail:    detail,
			Code:      code,
			Kind:      kind,
			TraceID:   traceID,
			RequestID: requestID,
			headers:   problemHeaders(status, e),
		}
	}
	return problemOf(CodeInternalError, "An internal error occurred.", requestID, traceID, nil)
}

func problemOf(code Code, detail, requestID, traceID string, items []ProblemItem) *Problem {
	info, ok := catalog[code]
	status := http.StatusInternalServerError
	title := "Internal error"
	kind := KindInternal
	if ok {
		status = info.Status
		if status == 0 {
			status = kindDefaultStatus(info.Kind)
		}
		title = info.Title
		kind = info.Kind
	}
	return &Problem{
		Type:      problemTypeBase + string(code),
		Title:     title,
		Status:    status,
		Detail:    detail,
		Code:      code,
		Kind:      kind,
		TraceID:   traceID,
		RequestID: requestID,
		Errors:    items,
	}
}
