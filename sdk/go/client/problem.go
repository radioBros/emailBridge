package client

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// ProblemItem is one entry in Problem.Errors (field-level validation).
type ProblemItem struct {
	Path    string `json:"path"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Problem is RFC 9457 problem details as returned by EmailBridge.
type Problem struct {
	Type      string        `json:"type"`
	Title     string        `json:"title"`
	Status    int           `json:"status"`
	Detail    string        `json:"detail,omitempty"`
	Instance  string        `json:"instance,omitempty"`
	Code      string        `json:"code"`
	Kind      string        `json:"kind"`
	TraceID   string        `json:"traceId,omitempty"`
	RequestID string        `json:"requestId,omitempty"`
	Errors    []ProblemItem `json:"errors,omitempty"`
}

// ProblemError is returned for non-2xx responses with problem+json (or a fallback).
type ProblemError struct {
	Problem Problem
	Header  http.Header
}

func (e *ProblemError) Error() string {
	if e.Problem.Detail != "" {
		return e.Problem.Detail
	}
	if e.Problem.Title != "" {
		return e.Problem.Title
	}
	return e.Problem.Code
}

func (e *ProblemError) StatusCode() int { return e.Problem.Status }

func parseProblem(body []byte, status int) Problem {
	var p Problem
	if len(body) > 0 {
		_ = json.Unmarshal(body, &p)
	}
	if p.Type == "" {
		p.Type = fmt.Sprintf("https://emailbridge.dev/problems/http_%d", status)
	}
	if p.Title == "" {
		p.Title = "Error"
	}
	if p.Status == 0 {
		p.Status = status
	}
	if p.Code == "" {
		p.Code = fmt.Sprintf("http_%d", status)
	}
	if p.Kind == "" {
		p.Kind = "temporary"
	}
	return p
}
