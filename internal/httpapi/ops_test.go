package httpapi

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func opPatch(path string) huma.Operation {
	return huma.Operation{OperationID: "patchThing", Method: http.MethodPatch, Path: path, Extensions: Phase("P0")}
}
