//go:build integration

package protolog_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/logging/query"
	logstore "github.com/radiobros/emailbridge/internal/logging/store"
	"github.com/radiobros/emailbridge/internal/protolog"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type env struct {
	svc    *accounts.Service
	api    *protolog.API
	sys    *logging.System
	db     *store.DB
	srv    *httptest.Server
	org    string
	orgB   string
	owner  context.Context
	admin  context.Context
	oper   context.Context
	viewer context.Context
}

func testEnv(t *testing.T) *env {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_protolog_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := accounts.EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	_ = jobs.Migration(1).Up(ctx, db)
	_ = audit.Migration(1).Up(ctx, db)

	ks := &crypto.MongoKeyStore{DB: db}
	_ = db.EnsureIndexes(ctx, crypto.DataKeysCollection, ks.Indexes()...)
	key := bytes.Repeat([]byte{9}, crypto.KeySize)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := &crypto.Cipher{KEK: l, Store: ks, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
	quiet := slog.New(slog.DiscardHandler)
	aud := &audit.Writer{DB: db, Log: quiet, Retention: 24 * time.Hour}
	svc := &accounts.Service{
		DB: db, Cipher: cipher, Events: &events.Writer{DB: db},
		Jobs: &jobs.Service{DB: db, NodeID: "n1", Log: quiet}, Audit: aud, Log: quiet,
		RawDebugTTL: time.Hour,
	}
	svc.RegisterHandlers()

	sys := logging.New(logging.Options{Node: "n1", Role: "api", Stdout: io.Discard, Fallback: io.Discard, FlushEvery: 5 * time.Millisecond})
	t.Cleanup(func() { _ = sys.Close(ctx) })
	ls, err := logstore.Open(ctx, logstore.Options{Dir: t.TempDir(), Node: "n1", SweepEvery: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ls.Close() })
	sys.AddSink(ls)
	hub := logstore.NewHub("n1", 0)
	t.Cleanup(hub.Close)
	qsvc, err := query.New(query.Options{Local: &logstore.Node{Store: ls, Hub: hub}, Log: quiet})
	if err != nil {
		t.Fatal(err)
	}

	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })

	api := &protolog.API{Accounts: svc, Logs: qsvc, Audit: svc, Log: quiet}
	hs, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	api.Register(hs)

	org := store.NewID(store.PrefixOrganization)
	orgB := store.NewID(store.PrefixOrganization)
	user := func(role tenancy.Role, o string) context.Context {
		return tenancy.WithCaller(ctx, tenancy.Caller{
			Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: o, Role: role,
		})
	}
	e := &env{
		svc: svc, api: api, sys: sys, db: db,
		org: org, orgB: orgB,
		owner: user(tenancy.RoleOwner, org), admin: user(tenancy.RoleAdmin, org),
		oper: user(tenancy.RoleOperator, org), viewer: user(tenancy.RoleViewer, org),
	}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cctx := e.owner
		switch r.Header.Get("X-Test-Role") {
		case "admin":
			cctx = e.admin
		case "operator":
			cctx = e.oper
		case "viewer":
			cctx = e.viewer
		case "orgB":
			cctx = user(tenancy.RoleOwner, orgB)
		}
		hs.ServeHTTP(w, r.WithContext(cctx))
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) createAccount(t *testing.T, ctx context.Context) *accounts.Account {
	t.Helper()
	a, err := e.svc.Create(ctx, accounts.CreateInput{
		Name: "P", Email: "p@example.com", SkipVerify: true,
		IMAP: accounts.IMAPConfigInput{Host: "127.0.0.1", Port: 993, TLS: accounts.TLSImplicit, Auth: accounts.MailAuthInput{User: "u", Pass: "secret-pass"}},
		SMTP: accounts.SMTPConfigInput{Host: "127.0.0.1", Port: 465, TLS: accounts.TLSImplicit, Auth: accounts.MailAuthInput{User: "u", Pass: "secret-pass"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (e *env) writeProtocol(t *testing.T, orgID, accountID string) {
	t.Helper()
	rec := &protolog.Recorder{Log: e.sys.For(logging.CatProtocol, "imap")}
	sink := rec.Sink(orgID, accountID, false)
	sink(protolog.Line{Dir: protolog.Client, Proto: "imap", ConnID: "c1", Text: "[masked]", Bytes: 8})
	sink(protolog.Line{Dir: protolog.Server, Proto: "imap", ConnID: "c1", Text: "a1 OK", Bytes: 5})
	sink(protolog.Line{Dir: protolog.Client, Proto: "smtp", ConnID: "c2", Text: "EHLO x", Bytes: 6})
	if err := e.sys.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *env) get(t *testing.T, role, path string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+path, nil)
	if role != "" {
		req.Header.Set("X-Test-Role", role)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestIntegrationProtocolLogsListDownloadRoles(t *testing.T) {
	e := testEnv(t)
	a := e.createAccount(t, e.owner)

	for _, tc := range []struct {
		ctx  context.Context
		role string
		ok   bool
	}{
		{e.viewer, "viewer", false},
		{e.oper, "operator", false},
		{e.admin, "admin", true},
		{e.owner, "owner", true},
	} {
		_, err := e.svc.Patch(tc.ctx, a.ID, accounts.PatchInput{
			Switches: map[string]any{"protocolLogs.enabled": true},
		}, "")
		if tc.ok && err != nil {
			t.Fatalf("%s: %v", tc.role, err)
		}
		if !tc.ok && errs.CodeOf(err) != errs.CodeForbidden {
			t.Fatalf("%s want forbidden, got %v", tc.role, err)
		}
	}

	a, err := e.svc.Get(e.owner, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if on, _ := a.Switches["protocolLogs.enabled"].(bool); !on {
		t.Fatalf("%+v", a.Switches)
	}

	e.writeProtocol(t, e.org, a.ID)

	from := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	code, body := e.get(t, "", fmt.Sprintf("/v1/accounts/%s/protocol-logs?from=%s&to=%s&limit=50", a.ID, from, to))
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var list protolog.ProtocolLogRecordList
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Data) < 3 {
		t.Fatalf("want ≥3 records, got %+v", list)
	}
	if strings.Contains(string(body), "secret-pass") {
		t.Fatal("credentials leaked in list")
	}

	code, body = e.get(t, "", fmt.Sprintf("/v1/accounts/%s/protocol-logs?from=%s&to=%s&proto=imap&connId=c1", a.ID, from, to))
	if code != 200 {
		t.Fatal(code, string(body))
	}
	_ = json.Unmarshal(body, &list)
	for _, r := range list.Data {
		if r.Proto != "imap" || r.ConnID != "c1" {
			t.Fatalf("filter leak %+v", r)
		}
	}

	code, _ = e.get(t, "viewer", fmt.Sprintf("/v1/accounts/%s/protocol-logs?from=%s&to=%s", a.ID, from, to))
	if code != 200 {
		t.Fatalf("viewer read %d", code)
	}

	code, _ = e.get(t, "orgB", fmt.Sprintf("/v1/accounts/%s/protocol-logs?from=%s&to=%s", a.ID, from, to))
	if code != 404 {
		t.Fatalf("tenant isolation %d", code)
	}

	before, _ := e.db.Collection(audit.Collection).CountDocuments(e.owner, bson.M{"action": "account.protocolLogs.download", "target.id": a.ID})
	code, body = e.get(t, "", fmt.Sprintf("/v1/accounts/%s/protocol-logs/download?from=%s&to=%s", a.ID, from, to))
	if code != 200 {
		t.Fatalf("download %d %s", code, body)
	}
	sc := bufio.NewScanner(bytes.NewReader(body))
	n := 0
	for sc.Scan() {
		n++
		if strings.Contains(sc.Text(), "secret-pass") {
			t.Fatal("credentials in download")
		}
		var rec protolog.ProtocolLogRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil || rec.ID == "" {
			t.Fatalf("ndjson %v %s", err, sc.Text())
		}
	}
	if n < 3 {
		t.Fatalf("download lines %d", n)
	}
	after, _ := e.db.Collection(audit.Collection).CountDocuments(e.owner, bson.M{"action": "account.protocolLogs.download", "target.id": a.ID})
	if after != before+1 {
		t.Fatalf("download audit %d → %d", before, after)
	}
}

func TestIntegrationRawDebugExpiresAndSyncSource(t *testing.T) {
	e := testEnv(t)
	a := e.createAccount(t, e.owner)
	e.svc.RawDebugTTL = 50 * time.Millisecond

	patched, err := e.svc.Patch(e.owner, a.ID, accounts.PatchInput{
		Switches: map[string]any{"protocolLogs.enabled": true, "protocolLogs.rawDebug": true},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if patched.RawDebugUntil == nil {
		t.Fatal("rawDebugUntil not set")
	}
	n, _ := e.db.Collection(audit.Collection).CountDocuments(e.owner, bson.M{
		"action": "account.protocolLogs.rawDebug.enable", "target.id": a.ID,
	})
	if n != 1 {
		t.Fatalf("raw debug enable audit %d", n)
	}
	if len(patched.PendingCommands) == 0 || patched.PendingCommands[len(patched.PendingCommands)-1].Cmd != accounts.CmdConfigChanged {
		t.Fatalf("configChanged missing: %+v", patched.PendingCommands)
	}

	ss := accounts.NewSyncSource(e.svc)
	loaded, err := ss.Load(context.Background(), a.ID)
	if err != nil || !loaded.ProtocolLogs || loaded.RawDebugUntil == nil {
		t.Fatalf("%+v %v", loaded, err)
	}

	time.Sleep(80 * time.Millisecond)
	got, err := e.svc.Get(e.owner, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.View().Switches["protocolLogs.rawDebug"] != false {
		t.Fatalf("view must show expired raw debug off: %+v", got.View().Switches)
	}
	loaded, err = ss.Load(context.Background(), a.ID)
	if err != nil || loaded.RawDebugUntil != nil {
		t.Fatalf("sync must drop expired raw debug: %+v %v", loaded, err)
	}
}
