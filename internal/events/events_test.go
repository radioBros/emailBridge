package events

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/trace"

	eventschemas "github.com/radiobros/emailbridge/api/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/store"
)

var (
	testOrg = store.NewID(store.PrefixOrganization)
	testAcc = store.NewID(store.PrefixAccount)
)

func received() Draft {
	return Draft{Type: MessageReceived, OrganizationID: testOrg, AccountID: testAcc, Subject: store.NewID(store.PrefixMessage),
		Data: &types.MessageReceived{AccountRef: types.AccountRef{AccountID: testAcc}, Subject: "hi", Flags: []string{}}}
}

func TestBuildEnvelope(t *testing.T) {
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{1}, SpanID: trace.SpanID{2}, TraceFlags: trace.FlagsSampled})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	e, err := Build(ctx, received())
	if err != nil {
		t.Fatal(err)
	}
	if e.SpecVersion != "1.0" || !store.IsID(store.PrefixEvent, e.ID) || e.Source != "/organizations/"+testOrg+"/accounts/"+testAcc ||
		e.DataSchema != SchemaBase+"message.received/v1.json" || e.EBSchema != 1 || e.EBOrg != testOrg || e.EBAccount != testAcc ||
		!strings.HasPrefix(e.TraceParent, "00-01000000") || e.Time.IsZero() || e.DataContentType != "application/json" {
		t.Fatalf("envelope %+v", e)
	}
	var data map[string]any
	if err := json.Unmarshal(e.Data, &data); err != nil || data["subject"] != "hi" || data["accountId"] != testAcc {
		t.Fatalf("data %s %v", e.Data, err)
	}
	if strings.Contains(string(e.Data), "externalId\":\"") {
		t.Fatal("externalId must be null when unset")
	}
	// organisation-level source, no trace
	d := received()
	d.AccountID = ""
	if e, err := Build(context.Background(), d); err != nil || e.Source != "/organizations/"+testOrg || e.TraceParent != "" {
		t.Fatalf("%+v %v", e, err)
	}
}

func TestBuildRejectsBadDrafts(t *testing.T) {
	cases := map[string]func(*Draft){
		"unknown type":  func(d *Draft) { d.Type = "message.nope" },
		"no org":        func(d *Draft) { d.OrganizationID = "" },
		"bad account":   func(d *Draft) { d.AccountID = "acc_x" },
		"wrong subject": func(d *Draft) { d.Subject = store.NewID(store.PrefixOutbound) },
		"wrong data":    func(d *Draft) { d.Data = &types.MessageSent{} },
		"nil data":      func(d *Draft) { d.Data = nil },
	}
	for name, mut := range cases {
		d := received()
		mut(&d)
		if _, err := Build(context.Background(), d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// value instead of pointer is fine
	d := received()
	d.Data = types.MessageReceived{}
	if _, err := Build(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	w := &Writer{}
	syn := Draft{Type: EndpointTest, OrganizationID: testOrg, Subject: store.NewID(store.PrefixEndpoint), Data: &types.EndpointTest{}}
	if _, err := w.Emit(context.Background(), syn); err == nil {
		t.Fatal("synthetic events are never stored")
	}
	if out, err := w.Emit(context.Background()); err != nil || out != nil {
		t.Fatal("empty emit")
	}
}

func TestCatalogue(t *testing.T) {
	// Phases add types (S3); the catalogue must contain at least the P1 set plus what later phases wired.
	have := map[string]bool{}
	for _, ct := range Types() {
		have[ct.Name] = true
	}
	for _, name := range []string{AccountAdded, AccountUpdated, AccountDeleted, AccountStateChanged, AccountInitialized,
		AccountAuthFailed, AccountAuthSucceeded, AccountConnectFailed, MessageReceived, MessageUpdated, MessageDeleted,
		MessageMissing, MessageSent, MessageDeliveryAttemptFailed, MessageFailed, MessageCaptured, EndpointTest,
		MessageBounced, MessageComplained} {
		if !have[name] {
			t.Fatalf("catalogue lacks %s", name)
		}
	}
	for i, ct := range Types() {
		if i > 0 && Types()[i-1].Name >= ct.Name {
			t.Fatal("not sorted")
		}
		if ct.Version != 1 || ct.Data == nil || !store.KnownPrefix(ct.Subject) {
			t.Fatalf("%+v", ct)
		}
	}
	if _, ok := Lookup("account.added"); !ok {
		t.Fatal("lookup")
	}
	if SubjectTokens("Message.Received") != "message.received" {
		t.Fatal("tokens")
	}
}

// TestSchemasUpToDate is the drift check: api/events must match the Go types (S3 §2).
func TestSchemasUpToDate(t *testing.T) {
	files, err := SchemaFiles()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range files {
		got, err := fs.ReadFile(eventschemas.FS, name)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s is out of date: run go run ./tools/eventschema", name)
		}
		var doc map[string]any
		if err := json.Unmarshal(want, &doc); err != nil || doc["$schema"] != jsonSchemaDialect || doc["$id"] == "" {
			t.Errorf("%s: bad header", name)
		}
		if bytes.Contains(want, []byte(`"additionalProperties": false`)) {
			t.Errorf("%s: closed content model breaks additive changes", name)
		}
	}
	entries, _ := fs.ReadDir(eventschemas.FS, ".")
	if len(entries) != len(files) {
		t.Errorf("api/events has %d files, generated %d (stale file?)", len(entries), len(files))
	}
}

func TestSchemaRoutes(t *testing.T) {
	r := chi.NewRouter()
	r.Get("/schemas/events/envelope.json", envelopeSchemaHandler)
	r.Get("/schemas/events/{type}/v{version}.json", typeSchemaHandler)
	srv := httptest.NewServer(r)
	defer srv.Close()
	for path, code := range map[string]int{
		"/schemas/events/message.received/v1.json": 200,
		"/schemas/events/envelope.json":            200,
		"/schemas/events/message.received/v2.json": 404,
		"/schemas/events/..%2fetc/v1.json":         404,
		"/schemas/events/message.received/v0.json": 404,
		"/schemas/events/MESSAGE.received/v1.json": 404,
	} {
		resp, err := http.Get(srv.URL + path) //nolint:noctx // test
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != code {
			t.Errorf("%s: %d want %d", path, resp.StatusCode, code)
		}
		if code == 200 && resp.Header.Get("Content-Type") != "application/schema+json" {
			t.Errorf("%s: content type %q", path, resp.Header.Get("Content-Type"))
		}
	}
}

func TestDocRoundTrip(t *testing.T) {
	e, err := Build(context.Background(), received())
	if err != nil {
		t.Fatal(err)
	}
	d := doc{ID: e.ID, OrganizationID: e.EBOrg, AccountID: e.EBAccount, Type: e.Type, Time: e.Time, Subject: e.Subject, Data: string(e.Data), Schema: 1}
	back := d.event()
	a, _ := json.Marshal(e)
	b, _ := json.Marshal(back)
	if !bytes.Equal(a, b) {
		t.Fatalf("round trip\n%s\n%s", a, b)
	}
	_ = time.Now
}
