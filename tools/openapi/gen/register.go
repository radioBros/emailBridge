package main

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// registerImplemented attaches every currently implemented huma operation. Tenancy does not yet
// export a Register helper (follow-up for Claude); the Operation values here mirror
// internal/tenancy.Module.Start so the generated document matches the live API.
func registerImplemented(s *httpapi.Server) {
	httpapi.Register(s, huma.Operation{
		OperationID: "getMe", Method: http.MethodGet, Path: "/v1/me", Summary: "Current caller",
		Tags: []string{"Me"}, Extensions: httpapi.Phase("P0"),
	}, func(context.Context, *struct{}) (*tenancy.MeOutput, error) {
		return &tenancy.MeOutput{}, nil
	})
}
