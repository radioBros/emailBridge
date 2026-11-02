package events

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"go.opentelemetry.io/otel/propagation"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// SchemaBase is where data schemas are published (S3 §2); every instance also serves them.
const SchemaBase = "https://emailbridge.dev/schemas/events/"

// Event is the CloudEvents 1.0 JSON envelope (S3 §1).
type Event struct {
	SpecVersion     string          `json:"specversion" doc:"CloudEvents version, always 1.0"`
	ID              string          `json:"id" doc:"Event ID (evt_…), unique and stable: deduplicate on it (deliveries are at least once)"`
	Type            string          `json:"type" doc:"Event type from the catalogue, e.g. message.received"`
	Source          string          `json:"source" doc:"/organizations/{orgId} or /organizations/{orgId}/accounts/{accountId}"`
	Subject         string          `json:"subject" doc:"ID of the resource the event is about (acc_…, msg_…, out_…, …)"`
	Time            time.Time       `json:"time" doc:"When the change happened (RFC 3339, millisecond precision, UTC)"`
	DataContentType string          `json:"datacontenttype" doc:"Media type of data, always application/json"`
	DataSchema      string          `json:"dataschema" doc:"URL of the JSON Schema of data (type and major version)"`
	EBOrg           string          `json:"eborg" doc:"Organisation ID (org_…)"`
	EBAccount       string          `json:"ebaccount,omitempty" doc:"Account ID (acc_…); omitted for organisation-level events"`
	EBSchema        int             `json:"ebschema" doc:"Major version of the data schema"`
	TraceParent     string          `json:"traceparent,omitempty" doc:"W3C trace context of the change that produced the event"`
	Data            json.RawMessage `json:"data" doc:"Event payload; its shape is the type's data schema (dataschema)"`
}

// Draft is what a producer hands to the Writer.
type Draft struct {
	Type           string
	OrganizationID string
	AccountID      string // "" for organisation-level events
	Subject        string // resource ID the event is about
	Time           time.Time
	Data           any // a value of the catalogue's Go type (pointer or value)
}

// build validates a draft and produces its envelope.
func build(ctx context.Context, d Draft) (Event, error) {
	ct, ok := Lookup(d.Type)
	if !ok {
		return Event{}, errs.New(errs.CodeInternalError, fmt.Sprintf("events: unknown type %q", d.Type))
	}
	if !store.IsID(store.PrefixOrganization, d.OrganizationID) {
		return Event{}, errs.New(errs.CodeInternalError, "events: organisation ID required")
	}
	if d.AccountID != "" && !store.IsID(store.PrefixAccount, d.AccountID) {
		return Event{}, errs.New(errs.CodeInternalError, "events: bad account ID")
	}
	if !store.IsID(ct.Subject, d.Subject) && (ct.AltSubject == "" || !store.IsID(ct.AltSubject, d.Subject)) {
		return Event{}, errs.New(errs.CodeInternalError, fmt.Sprintf("events: %s needs a %s_ subject, got %q", d.Type, ct.Subject, d.Subject))
	}
	dt := reflect.TypeOf(d.Data)
	if dt != nil && dt.Kind() == reflect.Pointer {
		dt = dt.Elem()
	}
	if dt != ct.Data {
		return Event{}, errs.New(errs.CodeInternalError, fmt.Sprintf("events: %s data must be %s, got %v", d.Type, ct.Data, dt))
	}
	data, err := json.Marshal(d.Data)
	if err != nil {
		return Event{}, errs.Wrap(err, errs.CodeInternalError, "events.marshal")
	}
	when := d.Time
	if when.IsZero() {
		when = time.Now()
	}
	src := "/organizations/" + d.OrganizationID
	if d.AccountID != "" {
		src += "/accounts/" + d.AccountID
	}
	return Event{
		SpecVersion: "1.0", ID: store.NewID(store.PrefixEvent), Type: d.Type, Source: src, Subject: d.Subject,
		Time: when.UTC().Truncate(time.Millisecond), DataContentType: "application/json",
		DataSchema: SchemaURL(d.Type, ct.Version), EBOrg: d.OrganizationID, EBAccount: d.AccountID,
		EBSchema: ct.Version, TraceParent: traceParent(ctx), Data: data,
	}, nil
}

// SchemaURL is the dataschema of a type and major version.
func SchemaURL(typ string, version int) string {
	return fmt.Sprintf("%s%s/v%d.json", SchemaBase, typ, version)
}

// traceParent extracts the W3C traceparent of the current span (S12 §10), "" without one.
func traceParent(ctx context.Context) string {
	c := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, c)
	return c.Get("traceparent")
}

// SubjectTokens is the NATS subject form of a type (dots are already token separators; S4 §2).
func SubjectTokens(typ string) string { return strings.ToLower(typ) }
