package httpapi

import (
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
)

// Documentation-only types for the raw health probes (the handlers in server.go write the same JSON).

// HealthzBody is the /healthz response.
type HealthzBody struct {
	Status string `json:"status" enum:"ok" doc:"Always ok while the process serves HTTP (liveness)." example:"ok"`
}

// ReadyCheck is one dependency result in /readyz.
type ReadyCheck struct {
	OK    bool   `json:"ok" doc:"Whether the dependency answered within 3 seconds." example:"false"`
	Error string `json:"error,omitempty" doc:"Why the check failed (only when ok is false)." example:"server selection error: context deadline exceeded"`
}

// ReadyzBody is the /readyz response (200 ready, 503 not ready).
type ReadyzBody struct {
	Status  string                `json:"status" enum:"ready,not_ready" doc:"ready when every dependency check passes, otherwise not_ready (HTTP 503)." example:"not_ready"`
	Checks  map[string]ReadyCheck `json:"checks,omitempty" doc:"Per-dependency results (store, bus, objstore, crypto …). Only shown to authenticated callers." example:"{\"bus\":{\"ok\":true},\"crypto\":{\"ok\":true},\"objstore\":{\"ok\":true},\"store\":{\"ok\":false,\"error\":\"server selection error: context deadline exceeded\"}}"`
	Failing []string              `json:"failing,omitempty" doc:"Names of the failing checks, for unauthenticated callers when not ready." example:"[\"store\"]"`
}

func (s *Server) documentProbes() {
	reg := s.API.OpenAPI().Components.Schemas
	healthz := reg.Schema(reflect.TypeFor[HealthzBody](), true, "HealthzBody")
	readyz := reg.Schema(reflect.TypeFor[ReadyzBody](), true, "ReadyzBody")
	noStore := map[string]*huma.Param{"Cache-Control": {Description: "Never cached.", Schema: &huma.Schema{Type: "string"}, Example: "no-store"}}
	s.Document(&huma.Operation{
		OperationID: "getHealthz", Method: http.MethodGet, Path: "/healthz", Tags: []string{"health"},
		Summary: "Liveness probe",
		Description: "Answers `200 {\"status\":\"ok\"}` as long as the process serves HTTP; it checks no dependency, so " +
			"orchestrators restart the container only when the process is wedged. Public (no authentication), never rate " +
			"limited, also served on `EB_HTTP_ADDR` by nodes without the api role. No side effects.",
		Security: []map[string][]string{},
		Responses: map[string]*huma.Response{
			"200": {Description: "The process is alive.", Content: map[string]*huma.MediaType{
				"application/json": {Schema: healthz, Example: map[string]any{"status": "ok"}},
			}},
		},
		Extensions: Phase("P0"),
	})
	s.Document(&huma.Operation{
		OperationID: "getReadyz", Method: http.MethodGet, Path: "/readyz", Tags: []string{"health"},
		Summary: "Readiness probe",
		Description: "Runs every dependency check (MongoDB `store`, NATS `bus`, object storage `objstore`, key material " +
			"`crypto`) with a 3-second timeout each and answers `200` when all pass, `503` otherwise. Public: anonymous " +
			"callers see only the status (and the names of failing checks); a signed-in session or API key also sees " +
			"each check's result. Load balancers should route traffic only to ready nodes. Responses are `Cache-Control: no-store`.",
		Security: []map[string][]string{},
		Responses: map[string]*huma.Response{
			"200": {Description: "Every dependency is ready.", Headers: noStore, Content: map[string]*huma.MediaType{
				"application/json": {Schema: readyz, Examples: map[string]*huma.Example{
					"anonymous":     {Summary: "Anonymous caller", Value: map[string]any{"status": "ready"}},
					"authenticated": {Summary: "Authenticated caller (with check details)", Value: map[string]any{"status": "ready", "checks": map[string]any{"bus": map[string]any{"ok": true}, "crypto": map[string]any{"ok": true}, "objstore": map[string]any{"ok": true}, "store": map[string]any{"ok": true}}}},
				}},
			}},
			"503": {Description: "At least one dependency is not ready (same body shape, not a problem document).", Headers: noStore, Content: map[string]*huma.MediaType{
				"application/json": {Schema: readyz, Examples: map[string]*huma.Example{
					"anonymous":     {Summary: "Anonymous caller", Value: map[string]any{"status": "not_ready", "failing": []any{"store"}}},
					"authenticated": {Summary: "Authenticated caller (with check details)", Value: map[string]any{"status": "not_ready", "checks": map[string]any{"bus": map[string]any{"ok": true}, "crypto": map[string]any{"ok": true}, "objstore": map[string]any{"ok": true}, "store": map[string]any{"ok": false, "error": "server selection error: context deadline exceeded"}}}},
				}},
			}},
		},
		Extensions: Phase("P0"),
	})
}
