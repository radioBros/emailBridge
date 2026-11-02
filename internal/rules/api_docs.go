package rules

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/httpapi"
)

// bodyFieldDoc is the OpenAPI description and example of one request body field.
type bodyFieldDoc struct {
	doc     string
	example any
}

// documentBodyFields adds descriptions (and examples) to the properties of an operation's JSON request body in
// the OpenAPI document (W70). It serves anonymous body structs, whose tags stay unchanged because struct tags
// are part of the type and tests build these bodies as struct literals. Documentation only.
func documentBodyFields(srv *httpapi.Server, method, path string, fields map[string]bodyFieldDoc) {
	oapi := srv.API.OpenAPI()
	item := oapi.Paths[path]
	if item == nil {
		return
	}
	var op *huma.Operation
	switch method {
	case http.MethodPost:
		op = item.Post
	case http.MethodPatch:
		op = item.Patch
	}
	if op == nil || op.RequestBody == nil || op.RequestBody.Content["application/json"] == nil {
		return
	}
	s := op.RequestBody.Content["application/json"].Schema
	if s != nil && s.Ref != "" && oapi.Components != nil && oapi.Components.Schemas != nil {
		s = oapi.Components.Schemas.SchemaFromRef(s.Ref)
	}
	if s == nil {
		return
	}
	for name, d := range fields {
		if p := s.Properties[name]; p != nil {
			p.Description = d.doc
			if d.example != nil {
				p.Examples = []any{d.example}
			}
		}
	}
}

// documentBuilderBodies documents the anonymous request bodies of the builder operations.
func documentBuilderBodies(srv *httpapi.Server) {
	documentBodyFields(srv, http.MethodPost, "/v1/routes/builder/compile", map[string]bodyFieldDoc{
		"builder": {doc: "The builder model to compile."},
	})
	documentBodyFields(srv, http.MethodPost, "/v1/routes/builder/expressible", map[string]bodyFieldDoc{
		"cel": {doc: "A CEL filter, typically one the builder emitted.",
			example: `(type == "message.received") && (data.subject.contains("invoice"))`},
	})
}
