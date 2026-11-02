package query

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/store"
)

// APIOptions configure Register.
type APIOptions struct {
	// System is the node's log system: settings are read from and applied to it. Without it the
	// settings operations are not registered.
	System *logging.System
	// Authorizer identifies callers (default TenantAuthorizer).
	Authorizer Authorizer
	// KeepAlive is the SSE keepalive interval (default 15 s).
	KeepAlive time.Duration
	// Log receives API-side failures (e.g. an export that broke mid-stream).
	Log *slog.Logger
}

type api struct {
	svc *Service
	o   APIOptions
}

const tagLogs = "logs"

func ext(scope string) map[string]any { return httpapi.WithScope(httpapi.Phase("P0"), scope) }

// Register adds the /v1/logs operations to srv (S2 §4.1, S12 §8).
func Register(srv *httpapi.Server, svc *Service, o APIOptions) {
	if o.Authorizer == nil {
		o.Authorizer = TenantAuthorizer{}
	}
	if o.KeepAlive <= 0 {
		o.KeepAlive = 15 * time.Second
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	a := &api{svc: svc, o: o}
	who := " Requires `logs.read` (owners, admins, operators; API keys `logs:read`) and sees only the active " +
		"organisation's records; instance owners (signed in) see every organisation and instance-level records."
	readProblems := httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing,
		errs.CodeValidationFailed, errs.CodeLogRangeTooLarge)
	httpapi.Register(srv, huma.Operation{OperationID: "listLogs", Method: http.MethodGet, Path: "/v1/logs", Tags: []string{tagLogs},
		Summary: "List log records",
		Description: "Searches the structured logs of every node (fan-out with a 5-second deadline; nodes that miss it " +
			"are listed in `partial`). Filters: `from` / `to` (required, span at most 31 days), `category` (repeat " +
			"or comma-separate), minimum `level`, correlation IDs (`requestId`, `traceId`, `eventId`, `deliveryId`, " +
			"`outboundId`), `accountId` and full-text `q` (phrase, last word as prefix). Newest first; `limit` " +
			"1–500 (default 50) with keyset `cursor`." + who + " Personal data is redacted at write time.",
		Metadata: readProblems,
		Responses: map[string]*huma.Response{"200": {Content: map[string]*huma.MediaType{"application/json": {Example: map[string]any{
			"data":       []any{logRecordExample, logRecordExample2},
			"nextCursor": "eyJ0aW1lIjoxNzkyODQ1MTMwMTIzNDU2Nzg5LCJpZCI6ImxvZ18wMW01ZGdtNjEwZnhuMXZqcDJtMTByNzU5ZiJ9",
		}}}}},
		Extensions: ext("logs:read")}, a.list)
	httpapi.Register(srv, huma.Operation{OperationID: "exportLogs", Method: http.MethodGet, Path: "/v1/logs/export", Tags: []string{tagLogs},
		Summary: "Export log records",
		Description: "Streams every record matching the filter (span at most 31 days) as NDJSON, one `LogRecord` per " +
			"line, newest first. Sent gzip-compressed when the request has `Accept-Encoding: gzip`. The filter is " +
			"validated before the stream starts, so errors are normal problem responses; a node failing mid-stream " +
			"truncates the export (logged server-side)." + who,
		Metadata:   readProblems,
		Extensions: ext("logs:read"), Responses: map[string]*huma.Response{"200": {Description: "NDJSON stream, one log record per line.",
			Headers: map[string]*huma.Param{
				"Content-Encoding": {Description: "gzip when the client accepts it.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "gzip"},
			},
			Content: map[string]*huma.MediaType{"application/x-ndjson": {Schema: &huma.Schema{Type: huma.TypeString},
				Example: logNDJSONExample}}}}}, a.export)
	httpapi.Register(srv, huma.Operation{OperationID: "getLogStats", Method: http.MethodGet, Path: "/v1/logs/stats", Tags: []string{tagLogs},
		Summary: "Log statistics",
		Description: "Counts the records in `[from, to)` (at most 31 days) by level and by category across the " +
			"cluster. Instance owners also get the disk usage per category and node. Used by the admin log " +
			"dashboard." + who,
		Metadata:   readProblems,
		Extensions: ext("logs:read")}, a.stats)
	if o.System != nil {
		httpapi.Register(srv, huma.Operation{OperationID: "getLogSettings", Method: http.MethodGet, Path: "/v1/logs/settings",
			Tags: []string{tagLogs}, Summary: "Log settings",
			Description: "Returns the runtime log settings of this node: stdout format, the disk cap, address hashing " +
				"and, per category, enabled / minimum level / retention / cap / sampling. Requires `logs.read`. " +
				"The `ETag` identifies this exact configuration.",
			Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing),
			Extensions: ext("logs:read")}, a.getSettings)
		httpapi.Register(srv, huma.Operation{OperationID: "patchLogSettings", Method: http.MethodPatch, Path: "/v1/logs/settings",
			Tags: []string{tagLogs}, Summary: "Update log settings",
			Description: "Changes the log settings (JSON Merge Patch: only the given fields change). Instance owners " +
				"only. Sizes are strings such as `5GiB` (each cap at least 1 MiB), retentions durations such as `14d` " +
				"(1 hour to 10 years), sampling fractions 0–1 per level (warn and above are always kept). Takes effect " +
				"immediately. `If-Match` (the `ETag` from GET, or `*`) guards against concurrent edits.",
			Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeValidationFailed,
				errs.CodeVersionMismatch),
			RequestBody: &huma.RequestBody{Content: map[string]*huma.MediaType{"application/json": {Examples: map[string]*huma.Example{
				"debugSync": {Summary: "Debug the sync category for a week", Value: map[string]any{
					"categories": map[string]any{"sync": map[string]any{"level": "debug", "retention": "7d", "sampling": map[string]any{"debug": 0.25}}},
				}},
				"global": {Summary: "JSON on stdout and a larger disk cap", Value: map[string]any{"stdout": "json", "diskCap": "10GiB"}},
			}}}},
			Extensions: ext("logs:write")}, a.patchSettings)
	}
	httpapi.Register(srv, huma.Operation{OperationID: "getLog", Method: http.MethodGet, Path: "/v1/logs/{logId}", Tags: []string{tagLogs},
		Summary: "Get one log record",
		Description: "Fetches one record by ID, asking every node (the ULID's day selects the file). `404` when no " +
			"node has it or it belongs to another organisation; `503` when it was not found but some node did not " +
			"answer (retry)." + who,
		Metadata: httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeNotFound,
			errs.CodeServiceUnavailable),
		Extensions: ext("logs:read")}, a.get)

	// The live tail is a raw handler (SSE needs flushing and keepalive comments); it is documented as a
	// regular operation so the OpenAPI document lists it.
	srv.Document(&huma.Operation{OperationID: "streamLogs", Method: http.MethodGet, Path: "/v1/logs/stream",
		Tags: []string{tagLogs}, Summary: "Live log tail (SSE)",
		Description: "Server-sent events with every new record that matches the filter, from all nodes, at most 50 " +
			"records per second per subscriber (slow clients drop records). Each event has `id: <log ID>` and " +
			"`data: <LogRecord JSON>`; a `:keepalive` comment is sent every 15 seconds. The stream starts with a " +
			"`: tail started` comment and has no history — use `GET /v1/logs` for the past." + who,
		Metadata:   httpapi.Problems(errs.CodeUnauthenticated, errs.CodeForbidden, errs.CodeScopeMissing, errs.CodeValidationFailed),
		Extensions: ext("logs:read"),
		Parameters: []*huma.Param{
			{Name: "category", In: "query", Explode: p(true), Description: "Category filter (repeat or comma-separate).",
				Schema: &huma.Schema{Type: huma.TypeArray, Items: &huma.Schema{Type: huma.TypeString}}, Example: []any{"sync", "send"}},
			{Name: "level", In: "query", Description: "Minimum level.", Example: "warn",
				Schema: &huma.Schema{Type: huma.TypeString, Enum: []any{"trace", "debug", "info", "warn", "error", "fatal"}}},
			{Name: "accountId", In: "query", Description: "Only records of this account.", Example: "acc_01m5cvnpb031g57sqct6mzhq94",
				Schema: &huma.Schema{Type: huma.TypeString}},
			{Name: "q", In: "query", Description: "Full-text match on message and attributes (at most 512 characters).",
				Example: "IDLE", Schema: &huma.Schema{Type: huma.TypeString}},
		},
		Responses: map[string]*huma.Response{"200": {Description: "SSE stream (text/event-stream).",
			Headers: map[string]*huma.Param{"Cache-Control": {Description: "Never cached.", Schema: &huma.Schema{Type: huma.TypeString}, Example: "no-store"}},
			Content: map[string]*huma.MediaType{"text/event-stream": {Schema: &huma.Schema{Type: huma.TypeString},
				Example: ": tail started\n\nid: log_01m5dgv610fxn1vjp2m10r759f\ndata: {\"id\":\"log_01m5dgv610fxn1vjp2m10r759f\",\"time\":1792845130123456789,\"level\":\"warn\",\"category\":\"sync\",\"component\":\"sync.imap\",\"msg\":\"IDLE connection dropped; reconnecting\",\"organizationId\":\"org_01m5caqpk0kast63yc13wpy2d9\",\"accountId\":\"acc_01m5cvnpb031g57sqct6mzhq94\"}\n\n:keepalive\n\n"}}}}})
	srv.Router.Get("/v1/logs/stream", a.stream)
}

// Example log records (documentation only).
var (
	logRecordExample = map[string]any{
		"id": "log_01m5dgv610fxn1vjp2m10r759f", "time": 1792845130123456789, "level": "warn", "category": "sync",
		"component": "sync.imap", "node": "eb-node-1", "role": "sync", "msg": "IDLE connection dropped; reconnecting",
		"organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "accountId": "acc_01m5cvnpb031g57sqct6mzhq94",
		"kind": "temporary", "code": "mail_connect_failed", "attrs": map[string]any{"mailbox": "INBOX", "attempt": 3},
	}
	logRecordExample2 = map[string]any{
		"id": "log_01m5dexqe0yceck9xvvwfm24j9", "time": 1792845011987654321, "level": "info", "category": "api",
		"component": "httpapi", "node": "eb-node-2", "role": "api", "msg": "request",
		"organizationId": "org_01m5caqpk0kast63yc13wpy2d9", "requestId": "req_01m5dexqe0yceck9xvvwfm24j9",
		"traceId": "4bf92f3577b34da6a3ce929d0e0e4736", "actor": "usr_01m5ccvej0dqefkn0xhkcq2q6z",
		"attrs": map[string]any{"method": "PATCH", "path": "/v1/accounts/acc_01m5cvnpb031g57sqct6mzhq94", "status": 200, "durationMs": 41},
	}
	logNDJSONExample = `{"id":"log_01m5dgv610fxn1vjp2m10r759f","time":1792845130123456789,"level":"warn","category":"sync","component":"sync.imap","msg":"IDLE connection dropped; reconnecting","organizationId":"org_01m5caqpk0kast63yc13wpy2d9","accountId":"acc_01m5cvnpb031g57sqct6mzhq94"}` + "\n" +
		`{"id":"log_01m5dexqe0yceck9xvvwfm24j9","time":1792845011987654321,"level":"info","category":"api","component":"httpapi","msg":"request","organizationId":"org_01m5caqpk0kast63yc13wpy2d9","requestId":"req_01m5dexqe0yceck9xvvwfm24j9"}` + "\n"
)

// --- list ---

type listLogsInput struct {
	Organization   string    `header:"EB-Organization" pattern:"^org_[0-9a-z]{26}$" doc:"Active organisation for session callers (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	Limit          int       `query:"limit" minimum:"1" maximum:"500" default:"50" doc:"Page size (1–500)." example:"100"`
	Cursor         string    `query:"cursor" doc:"Opaque base64url cursor (keyset pagination)." example:"eyJ0aW1lIjoxNzkyODQ1MTMwMTIzNDU2Nzg5LCJpZCI6ImxvZ18wMW01ZGdtNjEwZnhuMXZqcDJtMTByNzU5ZiJ9"`
	From           time.Time `query:"from" required:"true" doc:"Start (inclusive), RFC 3339" example:"2026-10-24T00:00:00Z"`
	To             time.Time `query:"to" required:"true" doc:"End (exclusive), RFC 3339" example:"2026-10-25T00:00:00Z"`
	Category       []string  `query:"category,explode" doc:"Category filter, may be repeated: system, api, auth, audit, event, sync, protocol, send, delivery, rules, job, cluster, integration, security, crash." example:"[\"sync\",\"send\"]"`
	Level          string    `query:"level" enum:"trace,debug,info,warn,error,fatal" doc:"Minimum level" example:"warn"`
	OrganizationID string    `query:"organizationId" pattern:"^org_[0-9a-z]{26}$" doc:"Instance owners only: records of this organisation (default: all)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	AccountID      string    `query:"accountId" doc:"Only records of this account." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	RequestID      string    `query:"requestId" doc:"Only records of this request (X-Request-Id)." example:"req_01m5dexqe0yceck9xvvwfm24j9"`
	EventID        string    `query:"eventId" doc:"Only records about this event." example:"evt_01m5ccvej0dqefkn0xhkcq2q6z"`
	DeliveryID     string    `query:"deliveryId" doc:"Only records about this webhook delivery." example:"dlv_01m5dsa5x0t7h8h17qhvq092rb"`
	OutboundID     string    `query:"outboundId" doc:"Only records about this outbound message." example:"out_01m5dq6dy05h78941bhj7x7pzr"`
	TraceID        string    `query:"traceId" doc:"Only records of this W3C trace." example:"4bf92f3577b34da6a3ce929d0e0e4736"`
	Q              string    `query:"q" maxLength:"512" doc:"Full-text match on msg/attrs (phrase, last word as prefix)" example:"IDLE connection"`
}

// LogRecordList is the list wrapper of GET /v1/logs.
type LogRecordList struct {
	Data       []store.LogRecord `json:"data" doc:"Records of this page, newest first."`
	NextCursor *string           `json:"nextCursor" required:"false" nullable:"true" doc:"Cursor of the next page; null on the last page." example:"eyJ0aW1lIjoxNzkyODQ1MTMwMTIzNDU2Nzg5LCJpZCI6ImxvZ18wMW01ZGdtNjEwZnhuMXZqcDJtMTByNzU5ZiJ9"`
	Partial    []string          `json:"partial,omitempty" doc:"Node IDs that missed the query deadline (results may be incomplete)." example:"[\"eb-node-3\"]"`
}

type listLogsOutput struct {
	Body LogRecordList
}

func categories(in []string) []logging.Category {
	var out []logging.Category
	for _, c := range in {
		for _, part := range strings.Split(c, ",") { // accept category=a,b as well as repeated parameters
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, logging.Category(part))
			}
		}
	}
	return out
}

func (a *api) list(ctx context.Context, in *listLogsInput) (*listLogsOutput, error) {
	org, _, err := scope(ctx, a.o.Authorizer, in.OrganizationID)
	if err != nil {
		return nil, err
	}
	page, err := a.svc.Search(ctx, Query{From: in.From, To: in.To, Categories: categories(in.Category), MinLevel: in.Level,
		OrganizationID: org, AccountID: in.AccountID, RequestID: in.RequestID, EventID: in.EventID, DeliveryID: in.DeliveryID,
		OutboundID: in.OutboundID, TraceID: in.TraceID, Q: in.Q, Limit: in.Limit, Cursor: in.Cursor})
	if err != nil {
		return nil, err
	}
	out := &listLogsOutput{Body: LogRecordList{Data: page.Records, Partial: page.Partial}}
	if page.NextCursor != "" {
		out.Body.NextCursor = &page.NextCursor
	}
	return out, nil
}

// --- get ---

type getLogInput struct {
	LogID        string `path:"logId" pattern:"^log_[0-9a-z]{26}$" doc:"Log record ID (log_…)." example:"log_01m5dgv610fxn1vjp2m10r759f"`
	Organization string `header:"EB-Organization" pattern:"^org_[0-9a-z]{26}$" doc:"Active organisation for session callers (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
}

type getLogOutput struct {
	Body store.LogRecord
}

func (a *api) get(ctx context.Context, in *getLogInput) (*getLogOutput, error) {
	org, owner, err := scope(ctx, a.o.Authorizer, "")
	if err != nil {
		return nil, err
	}
	r, partial, err := a.svc.Get(ctx, in.LogID)
	if err != nil {
		return nil, err
	}
	if r == nil || !visible(org, owner, r.OrganizationID) {
		if r == nil && len(partial) > 0 {
			return nil, errs.New(errs.CodeServiceUnavailable, "Not every node answered; the record may be on one of them. Retry later.")
		}
		return nil, errs.New(errs.CodeNotFound, "No log record with this ID.")
	}
	return &getLogOutput{Body: *r}, nil
}

// --- export ---

type exportLogsInput struct {
	Organization string    `header:"EB-Organization" pattern:"^org_[0-9a-z]{26}$" doc:"Active organisation for session callers (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	From         time.Time `query:"from" required:"true" doc:"Start (inclusive), RFC 3339" example:"2026-10-24T00:00:00Z"`
	To           time.Time `query:"to" required:"true" doc:"End (exclusive), RFC 3339" example:"2026-10-25T00:00:00Z"`
	Category     []string  `query:"category,explode" doc:"Category filter (repeat or comma-separate)." example:"[\"send\",\"delivery\"]"`
	Level        string    `query:"level" enum:"trace,debug,info,warn,error,fatal" doc:"Minimum level." example:"info"`
	AccountID    string    `query:"accountId" doc:"Only records of this account." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	Q            string    `query:"q" maxLength:"512" doc:"Full-text match on message and attributes." example:"bounce"`
}

func (a *api) export(ctx context.Context, in *exportLogsInput) (*huma.StreamResponse, error) {
	org, _, err := scope(ctx, a.o.Authorizer, "")
	if err != nil {
		return nil, err
	}
	q := Query{From: in.From, To: in.To, Categories: categories(in.Category), MinLevel: in.Level, OrganizationID: org,
		AccountID: in.AccountID, Q: in.Q, Limit: MaxLimit}
	if _, _, err := q.filter(); err != nil {
		return nil, err // validation before the stream starts, so the client gets a problem response
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		rctx := hctx.Context()
		hctx.SetHeader("Content-Type", "application/x-ndjson")
		w := hctx.BodyWriter()
		if acceptsGzip(hctx.Header("Accept-Encoding")) {
			hctx.SetHeader("Content-Encoding", "gzip")
			hctx.AppendHeader("Vary", "Accept-Encoding")
			gz := gzip.NewWriter(w)
			defer func() { _ = gz.Close() }()
			w = gz
		}
		n, partial, err := a.svc.Export(rctx, q, w)
		if err != nil || len(partial) > 0 {
			a.o.Log.LogAttrs(rctx, slog.LevelWarn, "log export incomplete", slog.Int64("records", n),
				slog.Any("partial", partial), slog.Any("err", err))
		}
	}}, nil
}

func acceptsGzip(h string) bool {
	for _, part := range strings.Split(h, ",") {
		enc, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") {
			return !strings.Contains(strings.ReplaceAll(params, " ", ""), "q=0")
		}
	}
	return false
}

// --- stats ---

type statsInput struct {
	Organization string    `header:"EB-Organization" pattern:"^org_[0-9a-z]{26}$" doc:"Active organisation for session callers (org_…)." example:"org_01m5caqpk0kast63yc13wpy2d9"`
	From         time.Time `query:"from" required:"true" doc:"Start (inclusive), RFC 3339" example:"2026-10-24T00:00:00Z"`
	To           time.Time `query:"to" required:"true" doc:"End (exclusive), RFC 3339" example:"2026-10-25T00:00:00Z"`
}

// LogStats is the OpenAPI LogStats body.
type LogStats struct {
	From       time.Time               `json:"from" doc:"Start of the counted range." example:"2026-10-24T00:00:00Z"`
	To         time.Time               `json:"to" doc:"End of the counted range (exclusive)." example:"2026-10-25T00:00:00Z"`
	ByLevel    map[string]int64        `json:"byLevel" doc:"Record count per level." example:"{\"debug\":0,\"info\":48211,\"warn\":312,\"error\":17}"`
	ByCategory map[string]int64        `json:"byCategory" doc:"Record count per category." example:"{\"api\":30544,\"sync\":12873,\"send\":4102,\"delivery\":1021}"`
	Disk       map[string]LogDiskUsage `json:"disk,omitempty" doc:"Disk usage per category (instance owners only)." example:"{\"sync\":{\"bytes\":734003200,\"nodes\":{\"eb-node-1\":367001600,\"eb-node-2\":367001600}}}"`
	Partial    []string                `json:"partial,omitempty" doc:"Node IDs that missed the query deadline." example:"[\"eb-node-3\"]"`
}

// LogDiskUsage is one category's disk usage, total and per node.
type LogDiskUsage struct {
	Bytes int64            `json:"bytes" doc:"Bytes on disk across all nodes." example:"734003200"`
	Nodes map[string]int64 `json:"nodes" doc:"Bytes per node ID." example:"{\"eb-node-1\":367001600,\"eb-node-2\":367001600}"`
}

type statsOutput struct {
	Body LogStats
}

func (a *api) stats(ctx context.Context, in *statsInput) (*statsOutput, error) {
	org, owner, err := scope(ctx, a.o.Authorizer, "")
	if err != nil {
		return nil, err
	}
	st, err := a.svc.Stats(ctx, in.From, in.To, org)
	if err != nil {
		return nil, err
	}
	out := LogStats{From: in.From.UTC(), To: in.To.UTC(), ByLevel: st.ByLevel, ByCategory: st.ByCategory, Partial: st.Partial}
	if owner { // disk usage is instance-level information
		out.Disk = map[string]LogDiskUsage{}
		for c, nodes := range st.Disk {
			u := LogDiskUsage{Nodes: nodes}
			for _, n := range nodes {
				u.Bytes += n
			}
			out.Disk[c] = u
		}
	}
	return &statsOutput{Body: out}, nil
}

// --- settings ---

type getSettingsInput struct{}

type settingsOutput struct {
	ETag string `header:"ETag" doc:"Identifies the current log configuration; send as If-Match on PATCH." example:"\"v3f9a1c2e7b4d6a80\""`
	Body LogSettings
}

func (a *api) getSettings(ctx context.Context, _ *getSettingsInput) (*settingsOutput, error) {
	if _, _, err := a.o.Authorizer.Caller(ctx); err != nil {
		return nil, err
	}
	s := settingsToAPI(a.o.System.Settings())
	return &settingsOutput{ETag: etag(s), Body: s}, nil
}

type patchSettingsInput struct {
	IfMatch string `header:"If-Match" doc:"Optional optimistic concurrency: the ETag from GET, or * for any." example:"\"v3f9a1c2e7b4d6a80\""`
	Body    LogSettings
}

func (a *api) patchSettings(ctx context.Context, in *patchSettingsInput) (*settingsOutput, error) {
	_, owner, err := a.o.Authorizer.Caller(ctx)
	if err != nil {
		return nil, err
	}
	if !owner {
		return nil, errs.New(errs.CodeForbidden, "Only instance owners may change the log settings.")
	}
	cur := a.o.System.Settings()
	if in.IfMatch != "" && strings.TrimSpace(in.IfMatch) != "*" && strings.TrimPrefix(strings.TrimSpace(in.IfMatch), "W/") != etag(settingsToAPI(cur)) {
		return nil, versionMismatch()
	}
	next, err := applyPatch(cur, in.Body)
	if err != nil {
		return nil, err
	}
	a.o.System.Apply(next)
	s := settingsToAPI(a.o.System.Settings())
	return &settingsOutput{ETag: etag(s), Body: s}, nil
}

// --- stream (SSE) ---

func (a *api) stream(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	org, _, err := scope(ctx, a.o.Authorizer, "")
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	qv := r.URL.Query()
	tf := TailFilter{Categories: categories(qv["category"]), MinLevel: qv.Get("level"), OrganizationID: org,
		AccountID: qv.Get("accountId"), Q: qv.Get("q")}
	records := make(chan store.LogRecord, 256)
	stop, err := a.svc.Tail(ctx, tf, func(rec store.LogRecord) {
		select {
		case records <- rec:
		default: // the client is too slow; the rate cap keeps this rare
		}
	})
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	defer stop()
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(w, ": tail started\n\n"); err != nil {
		return
	}
	_ = rc.Flush()
	keep := time.NewTicker(a.o.KeepAlive)
	defer keep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keep.C:
			if _, err := io.WriteString(w, ":keepalive\n\n"); err != nil {
				return
			}
			_ = rc.Flush()
		case rec := <-records:
			b, err := json.Marshal(&rec)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\ndata: %s\n\n", rec.ID, b); err != nil {
				return
			}
			_ = rc.Flush()
		}
	}
}
