//go:build integration

package gateways

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_gwy_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func testCipher(t *testing.T, db *store.DB) *crypto.Cipher {
	t.Helper()
	ctx := context.Background()
	ks := &crypto.MongoKeyStore{DB: db}
	if err := db.EnsureIndexes(ctx, crypto.DataKeysCollection, ks.Indexes()...); err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{7}, crypto.KeySize)
	l, err := crypto.NewLocal([]crypto.LocalKey{{ID: "k1", Key: key}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return &crypto.Cipher{KEK: l, Store: ks, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
}

func TestIntegrationCRUDAndVerify(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), Egress: egress.NewDialer(egress.Policy{}),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour}, EHLO: "eb.test",
	}

	sim := simtest.Start(t)
	const user, pass = "gwy@example.test", "s3cret-sim"
	if err := sim.Client.CreateUser(ctx, user, pass); err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(sim.Addr.SMTPPlain)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	g, err := svc.Create(cctx, CreateInput{
		Name: "relay", Host: host, Port: port, TLS: TLSNone,
		Auth: AuthInput{User: user, Pass: pass}, EHLOName: "gateway.ehlo.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !store.IsID(store.PrefixGateway, g.ID) || g.Auth.Pass.IsZero() {
		t.Fatalf("created: %+v", g)
	}
	if _, err := svc.Create(cctx, CreateInput{
		Name: "relay", Host: host, Port: port, TLS: TLSNone, Auth: AuthInput{User: user, Pass: pass},
	}); errs.CodeOf(err) != errs.CodeGatewayNameTaken {
		t.Fatalf("dup name: %v", err)
	}

	got, err := svc.Get(cctx, g.ID)
	if err != nil || got.Name != "relay" {
		t.Fatalf("get: %v %+v", err, got)
	}
	list, err := svc.List(cctx, ListFilter{IncludeTotal: true, Page: 1})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("list: %v %+v", err, list)
	}
	name := "relay-2"
	patched, err := svc.Patch(cctx, g.ID, formatETag(g.Version), PatchInput{Name: &name})
	if err != nil || patched.Name != "relay-2" || patched.Version != 2 {
		t.Fatalf("patch: %v %+v", err, patched)
	}

	vr, err := svc.Verify(cctx, g.ID)
	if err != nil || !vr.OK {
		t.Fatalf("verify: %v %+v", err, vr)
	}
	ds, err := svc.DialSettings(ctx, org, g.ID)
	if err != nil || ds.Password != pass || ds.EHLOName != "gateway.ehlo.test" {
		t.Fatalf("dial: %v %+v", err, ds)
	}
	off := false
	if _, err := svc.Patch(cctx, g.ID, formatETag(patched.Version), PatchInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DialSettings(ctx, org, g.ID); errs.CodeOf(err) != errs.CodeGatewayUnavailable {
		t.Fatalf("disabled: %v", err)
	}
	on := true
	cur, _ := svc.Get(cctx, g.ID)
	if _, err := svc.Patch(cctx, g.ID, formatETag(cur.Version), PatchInput{Enabled: &on}); err != nil {
		t.Fatal(err)
	}
	cur, _ = svc.Get(cctx, g.ID)
	if err := svc.Delete(cctx, g.ID, formatETag(cur.Version)); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(cctx, g.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("deleted: %v", err)
	}
}
