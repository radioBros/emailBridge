package events

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
)

const tagEvents = "events"

// SwitchResolver resolves effective feature switches (satisfied by *settings.Service).
type SwitchResolver interface {
	Effective(ctx context.Context, orgID string) (settings.SettingsBody, error)
}

// APIOptions configure Register.
type APIOptions struct {
	// Settings resolves the events.sse switch (default on when nil).
	Settings SwitchResolver
	// SSEEnabled overrides the switch lookup (tests). nil → Settings / default on.
	SSEEnabled func(ctx context.Context, orgID string) bool
	// KeepAlive is the SSE keepalive interval (default 15 s; tests may shorten it).
	KeepAlive time.Duration
	// MaxStreams is the per-organisation concurrent SSE cap on this node (default 20).
	MaxStreams int
	// StreamBuffer is the live fan-in buffer size; a full buffer disconnects the client (default 256).
	StreamBuffer int
	// Subscriber is the JetStream fan-in (required for the stream).
	Subscriber StreamSubscriber
}

type api struct {
	w   *Writer
	o   APIOptions
	lim *streamLimiter
}

func p(v bool) *bool { return &v }

// Register adds the /v1/events operations (S2 §4.4, S7 §7).
func Register(srv *httpapi.Server, w *Writer, o APIOptions) {
	if o.KeepAlive <= 0 {
		o.KeepAlive = 15 * time.Second
	}
	if o.MaxStreams <= 0 {
		o.MaxStreams = 20
	}
	if o.StreamBuffer <= 0 {
		o.StreamBuffer = 256
	}
	a := &api{w: w, o: o, lim: newStreamLimiter(o.MaxStreams)}
	phase := httpapi.Phase("P3")
	httpapi.Register(srv, huma.Operation{
		OperationID: "listEvents", Method: http.MethodGet, Path: "/v1/events",
		Summary: "List events", Tags: []string{tagEvents},
		Description: "Lists the stored CloudEvents envelopes of the active organisation, the same bodies webhook " +
			"endpoints receive (data schemas: `/schemas/events/{type}/v{N}.json`). Events are kept for the event " +
			"retention period. Filters: `type` (repeatable or comma list; `message.*` matches a whole family), " +
			"`accountId`, and the time range `from` / `to` (inclusive, RFC 3339). An account-bound API key sees only " +
			"its account's events. Pagination: `limit` (default 50, max 200) with `cursor` from `nextCursor`, or " +
			"`page` with `includeTotal`; sort `time` or `id` (prefix `-` for descending, default `-time`). Requires " +
			"the `read` permission (API keys `events:read`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeSortNotAllowed},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "type wildcards must be <family>.* (one token)."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleEventList())}},
		Extensions: httpapi.WithScope(phase, "events:read"),
	}, a.list)
	httpapi.Register(srv, huma.Operation{
		OperationID: "getEvent", Method: http.MethodGet, Path: "/v1/events/{eventId}",
		Summary: "Get one event", Tags: []string{tagEvents},
		Description: "Returns one stored CloudEvents envelope by ID, e.g. to look up the event of a webhook delivery. " +
			"`404 not_found` outside the organisation, after the retention period, and for synthetic events " +
			"(`endpoint.test` is never stored). An account-bound API key gets `403 forbidden` for another account's " +
			"event. Requires the `read` permission (API keys `events:read`). Read-only.",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeNotFound, Detail: "The event does not exist."}),
		Responses:  map[string]*huma.Response{"200": {Content: jsonContent(exampleEventEnvelope(MessageReceived))}},
		Extensions: httpapi.WithScope(phase, "events:read"),
	}, a.get)

	// SSE is a raw handler (flushing + keepalive); documented via Server.Document like /v1/logs/stream.
	srv.Document(&huma.Operation{
		OperationID: "streamEvents", Method: http.MethodGet, Path: "/v1/events/stream",
		Tags: []string{tagEvents}, Summary: "Live event stream (SSE)",
		Description: "Server-Sent Events of CloudEvents envelopes. Frames: `id: <eventId>`, `event: <type>`, " +
			"`data: <CloudEvents JSON>`; `:keepalive` every 15 s. Switch `events.sse` (default on; off: " +
			"`403 feature_disabled`). Requires the `read` permission (API keys `events:read`); an account-bound key " +
			"streams only its account. Filters: `type` (repeatable or comma list, `message.*` wildcards) and " +
			"`accountId`.\n\n" +
			"**Resume** (`Last-Event-ID` header or `lastEventId` query) uses an at-least-once algorithm because " +
			"events of different partitions can commit slightly out of ID order:\n" +
			"1. Start the live subscription first (deliver-new on EMAILBRIDGE_EVENTS), buffering into a bounded channel.\n" +
			"2. Catch up from the store: events with `time ≥ (time of Last-Event-ID − 5 s)` in `_id` order; " +
			"clients may see a few events again and must dedupe by `id`.\n" +
			"3. Switch to live: emit buffered and new events whose ID was not already sent during catch-up.\n" +
			"Without Last-Event-ID: live only. A Last-Event-ID older than retention → `422 replay_window_exceeded`. " +
			"At most 20 concurrent streams per organisation per node (`429 rate_limited`). A slow client whose " +
			"buffer fills is disconnected (resume with Last-Event-ID).",
		Metadata: httpapi.ProblemDocs(httpapi.ProblemDoc{Code: errs.CodeUnauthenticated},
			httpapi.ProblemDoc{Code: errs.CodeForbidden, Detail: "The API key is bound to a different account."},
			httpapi.ProblemDoc{Code: errs.CodeScopeMissing},
			httpapi.ProblemDoc{Code: errs.CodeFeatureDisabled, Detail: "Event streaming is disabled for this organisation."},
			httpapi.ProblemDoc{Code: errs.CodeValidationFailed, Detail: "Last-Event-ID is not a valid event id."},
			httpapi.ProblemDoc{Code: errs.CodeReplayWindowExceeded, Detail: "Last-Event-ID is outside the retention window."},
			httpapi.ProblemDoc{Code: errs.CodeRateLimited, Detail: "Too many concurrent event streams for this organisation on this node."},
			httpapi.ProblemDoc{Code: errs.CodeServiceUnavailable, Detail: "Event streaming is not available on this node."}),
		Extensions: httpapi.WithScope(phase, "events:read"),
		Parameters: []*huma.Param{
			{Name: "type", In: "query", Explode: p(true), Schema: &huma.Schema{Type: huma.TypeArray, Items: &huma.Schema{Type: huma.TypeString}},
				Description: "Event type filter (repeatable or comma list; `message.*` wildcards).",
				Example:     []any{"message.received", "message.sent"}},
			{Name: "accountId", In: "query", Schema: &huma.Schema{Type: huma.TypeString, Pattern: "^acc_[0-9a-z]{26}$"},
				Description: "Only events of this account (acc_…).", Example: exAccount},
			{Name: "lastEventId", In: "query", Schema: &huma.Schema{Type: huma.TypeString, Pattern: "^evt_[0-9a-z]{26}$"},
				Description: "Resume cursor for clients that cannot set Last-Event-ID.",
				Example:     eventExamples[MessageReceived].ID},
			{Name: "Last-Event-ID", In: "header", Schema: &huma.Schema{Type: huma.TypeString}},
		},
		Responses: map[string]*huma.Response{"200": {Description: "SSE stream",
			Content: map[string]*huma.MediaType{"text/event-stream": {Schema: &huma.Schema{Type: huma.TypeString},
				Example: exampleSSEFrames()}}}},
	})
	srv.Router.Get("/v1/events/stream", a.stream)
}

type listEventsInput struct {
	Limit        int       `query:"limit" minimum:"1" maximum:"200" default:"50" doc:"Page size (default 50, at most 200)." example:"50"`
	Cursor       string    `query:"cursor" doc:"Opaque cursor from the previous page's nextCursor." example:"eyJpZCI6ImV2dF8wMW01ZDIweTgwbjRwczBhcHozZTZiYXFxMyIsImZpZWxkIjoidGltZSIsImRlc2MiOnRydWV9"`
	Page         int       `query:"page" minimum:"0" doc:"1-based page number (offset paging; use instead of cursor)." example:"1"`
	IncludeTotal bool      `query:"includeTotal" doc:"With page: also return total (capped at 10 000)." example:"true"`
	Sort         string    `query:"sort" doc:"Whitelist: time, id. Prefix - for descending. Default -time." example:"-time"`
	Type         []string  `query:"type,explode" doc:"Exact types and/or family.* wildcards" example:"message.*"`
	AccountID    string    `query:"accountId" pattern:"^acc_[0-9a-z]{26}$" doc:"Only events of this account (acc_…)." example:"acc_01m5cvnpb031g57sqct6mzhq94"`
	From         time.Time `query:"from" doc:"Start (inclusive), RFC 3339" example:"2026-10-27T00:00:00Z"`
	To           time.Time `query:"to" doc:"End (inclusive), RFC 3339" example:"2026-10-27T23:59:59Z"`
}

type listEventsOutput struct {
	Body List
}

func (a *api) list(ctx context.Context, in *listEventsInput) (*listEventsOutput, error) {
	result, err := a.w.List(ctx, ListFilter{
		Limit: in.Limit, Cursor: in.Cursor, Page: in.Page, IncludeTotal: in.IncludeTotal,
		Sort: in.Sort, Type: in.Type, AccountID: in.AccountID, From: in.From, To: in.To,
	})
	if err != nil {
		return nil, err
	}
	if result.Data == nil {
		result.Data = []Event{}
	}
	return &listEventsOutput{Body: *result}, nil
}

type getEventInput struct {
	EventID string `path:"eventId" pattern:"^evt_[0-9a-z]{26}$" doc:"Event ID (evt_…)." example:"evt_01m5czx69032gsbkp82n4ajd1w"`
}

type getEventOutput struct {
	Body Event
}

func (a *api) get(ctx context.Context, in *getEventInput) (*getEventOutput, error) {
	e, err := a.w.GetEvent(ctx, in.EventID)
	if err != nil {
		return nil, err
	}
	return &getEventOutput{Body: e}, nil
}

func (a *api) sseEnabled(ctx context.Context, orgID string) bool {
	if a.o.SSEEnabled != nil {
		return a.o.SSEEnabled(ctx, orgID)
	}
	if a.o.Settings == nil {
		return true
	}
	body, err := a.o.Settings.Effective(ctx, orgID)
	if err != nil {
		return true
	}
	sv, ok := body.Switches["events.sse"]
	if !ok {
		return true
	}
	b, ok := sv.Value.(bool)
	if !ok {
		return true
	}
	return b
}
