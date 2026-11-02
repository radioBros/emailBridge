package mcp

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
)

// document adds the raw /mcp handler to the OpenAPI document (W70). Documentation only.
func (h *Handler) document(srv *httpapi.Server) {
	srv.Document(&huma.Operation{
		OperationID: "mcpRequest", Method: http.MethodPost, Path: "/mcp",
		Summary: "Model Context Protocol endpoint", Tags: []string{"mcp"},
		Description: "Model Context Protocol server over streamable HTTP (JSON-RPC 2.0) for AI assistants and agents. " +
			"Only API keys with the `mcp:use` scope may connect (session sign-ins get `403 forbidden`), and the " +
			"`mcp.enabled` switch must be on for the organisation (`403 feature_disabled`). Every tool call is " +
			"checked against the key's other scopes, exactly like the REST API. Tools: `list_accounts`, " +
			"`list_messages`, `search_messages`, `get_message` (text only by default), `send_message` (needs " +
			"`outbound:write`; no attachments), `list_deliveries`, `list_events` and the AI plugin tools " +
			"`ai_summarize`, `ai_classify`, `ai_extract`, `ai_risk`. Start with `initialize`, then `tools/list` and " +
			"`tools/call`; the server answers with JSON or an SSE stream, as the transport negotiates. GET (server " +
			"stream) and DELETE (end session) on the same path follow the MCP transport specification.",
		Metadata: httpapi.ProblemDocs(
			httpapi.ProblemDoc{Code: errs.CodeUnauthenticated, Detail: "Authentication is required."},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "MCP requires an API key."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing, Detail: "The API key lacks mcp:use."},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "The MCP endpoint is disabled for this organisation."}),
		RequestBody: &huma.RequestBody{Required: true, Content: map[string]*huma.MediaType{"application/json": {
			Schema: &huma.Schema{Type: huma.TypeObject, Description: "A JSON-RPC 2.0 request of the MCP protocol."},
			Examples: map[string]*huma.Example{
				"initialize": {Summary: "Open a session", Value: map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize",
					"params": map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
						"clientInfo": map[string]any{"name": "crm-assistant", "version": "2.3.0"}}}},
				"toolsCall": {Summary: "Call a tool", Value: map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call",
					"params": map[string]any{"name": "search_messages", "arguments": map[string]any{
						"accountId": "acc_01m5cvnpb031g57sqct6mzhq94", "subject": "invoice", "from": "example.org"}}}},
			},
		}}},
		Responses: map[string]*huma.Response{"200": {
			Description: "JSON-RPC response (application/json), or an SSE stream of JSON-RPC messages (text/event-stream).",
			Content: map[string]*huma.MediaType{
				"application/json": {
					Schema: &huma.Schema{Type: huma.TypeObject, Description: "A JSON-RPC 2.0 response."},
					Example: map[string]any{"jsonrpc": "2.0", "id": 7, "result": map[string]any{"content": []any{
						map[string]any{"type": "text", "text": `[{"id":"msg_01m5ch2yg09swc93g54trswp15","subject":"Invoice 2026-118","from":"giulia.bianchi@example.org"}]`},
					}}},
				},
				"text/event-stream": {
					Schema: &huma.Schema{Type: huma.TypeString, Description: "SSE frames, each carrying one JSON-RPC message."},
					Example: "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\"," +
						"\"serverInfo\":{\"name\":\"emailbridge\",\"version\":\"1.0.0\"},\"capabilities\":{\"tools\":{}}}}\n\n",
				},
			},
		}},
		Extensions: httpapi.WithScope(httpapi.Phase("P7"), "mcp:use"),
	})
}
