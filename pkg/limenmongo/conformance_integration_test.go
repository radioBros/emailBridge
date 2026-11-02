//go:build integration

package limenmongo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/thecodearcher/limen"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

// testAdapter connects to EB_TEST_MONGO_URI (a replica set) with a fresh database that is dropped after
// the test; it skips when no MongoDB is reachable.
func testAdapter(t *testing.T, opts ...Option) *Adapter {
	t.Helper()
	uri := testURI()
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(3 * time.Second))
	if err != nil {
		t.Skipf("no MongoDB at %s: %v", uri, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = client.Disconnect(context.Background())
		t.Skipf("no MongoDB at %s: %v", uri, err)
	}
	db := client.Database(fmt.Sprintf("limenmongo_%d", time.Now().UnixNano()))
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	a, err := New(db, opts...)
	if err != nil {
		t.Fatal(err)
	}
	ictx, icancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer icancel()
	if err := a.EnsureIndexes(ictx); err != nil {
		t.Fatal(err)
	}
	return a
}

func create(t *testing.T, a limen.DatabaseAdapter, table limen.SchemaTableName, row map[string]any) {
	t.Helper()
	res, err := a.Create(context.Background(), table, row)
	if err != nil || res.RowsAffected != 1 {
		t.Fatalf("create %s: %v (%d)", table, err, res.RowsAffected)
	}
}

func TestConformanceCreateFindDefaults(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	a := testAdapter(t, withClock(func() time.Time { return now }))
	ctx := context.Background()
	email := "Admin@Example.test"
	var nilTime *time.Time
	create(t, a, "users", map[string]any{"id": "u1", "email": &email, "password": nil, "email_verified_at": nilTime})
	row, err := a.FindOne(ctx, "users", []limen.Where{limen.Eq("id", "u1")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if row["id"] != "u1" || row["email"] != email {
		t.Fatalf("row = %v", row)
	}
	if row["two_factor_enabled"] != false {
		t.Fatalf("default two_factor_enabled = %#v", row["two_factor_enabled"])
	}
	if ca, ok := row["created_at"].(time.Time); !ok || !ca.Equal(now) || ca.Location() != time.UTC {
		t.Fatalf("default created_at = %#v", row["created_at"])
	}
	if row["password"] != nil || row["email_verified_at"] != nil {
		t.Fatalf("nil values = %#v %#v", row["password"], row["email_verified_at"])
	}
	// typed defaults Limen asserts on
	create(t, a, "passkeys", map[string]any{"id": "p1", "user_id": "u1", "credential_id": "c", "public_key": "k", "aaguid": "g"})
	pk, _ := a.FindOne(ctx, "passkeys", []limen.Where{limen.Eq("id", "p1")}, nil)
	if pk["sign_count"] != int64(0) || pk["backup_eligible"] != false || pk["backup_state"] != false {
		t.Fatalf("passkey defaults = %#v", pk)
	}
	// a missing id gets a UUID
	create(t, a, "verifications", map[string]any{"subject": "s", "value": "v", "expires_at": now.Add(time.Hour)})
	v, _ := a.FindOne(ctx, "verifications", []limen.Where{limen.Eq("value", "v")}, nil)
	if id, ok := v["id"].(string); !ok || len(id) != 36 {
		t.Fatalf("generated id = %#v", v["id"])
	}
	if _, err := a.Create(ctx, "users", map[string]any{}); err == nil {
		t.Fatal("empty create must fail")
	}
	if _, err := a.FindOne(ctx, "users", []limen.Where{limen.Eq("id", "missing")}, nil); !errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatalf("miss = %v", err)
	}
	// unknown tables are stored untyped
	create(t, a, "custom", map[string]any{"n": 3, "m": map[string]any{"a": "b"}})
	c, _ := a.FindOne(ctx, "custom", nil, nil)
	if !reflect.DeepEqual(c["m"], map[string]any{"a": "b"}) {
		t.Fatalf("custom = %#v", c)
	}
}

func seedNumbers(t *testing.T, a *Adapter) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, k := range []string{"alpha", "beta", "gamma", "a.b", "axb"} {
		create(t, a, "rate_limits", map[string]any{"id": fmt.Sprintf("r%d", i), "key": k, "count": i, "last_request_at": base.Add(time.Duration(i) * time.Hour).UnixMilli()})
	}
}

func ids(rows []map[string]any) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r["id"].(string)
	}
	return out
}

func TestConformanceOperators(t *testing.T) {
	a := testAdapter(t)
	seedNumbers(t, a)
	ctx := context.Background()
	asc := &limen.QueryOptions{OrderBy: []limen.OrderBy{{Column: "id"}}}
	cases := []struct {
		name  string
		where []limen.Where
		want  []string
	}{
		{"eq", []limen.Where{limen.Eq("key", "beta")}, []string{"r1"}},
		{"ne", []limen.Where{{Column: "key", Operator: limen.OpNe, Value: "beta"}}, []string{"r0", "r2", "r3", "r4"}},
		{"lt", []limen.Where{{Column: "count", Operator: limen.OpLt, Value: 2}}, []string{"r0", "r1"}},
		{"lte", []limen.Where{{Column: "count", Operator: limen.OpLte, Value: int64(2)}}, []string{"r0", "r1", "r2"}},
		{"gt", []limen.Where{{Column: "count", Operator: limen.OpGt, Value: 3}}, []string{"r4"}},
		{"gte", []limen.Where{{Column: "count", Operator: limen.OpGte, Value: 3}}, []string{"r3", "r4"}},
		{"in", []limen.Where{{Column: "key", Operator: limen.OpIn, Value: []string{"alpha", "gamma"}}}, []string{"r0", "r2"}},
		{"in any", []limen.Where{{Column: "id", Operator: limen.OpIn, Value: []any{"r1", "r4"}}}, []string{"r1", "r4"}},
		{"not_in", []limen.Where{{Column: "key", Operator: limen.OpNotIn, Value: []string{"alpha", "gamma"}}}, []string{"r1", "r3", "r4"}},
		{"contains escaped", []limen.Where{{Column: "key", Operator: limen.OpContains, Value: "."}}, []string{"r3"}},
		{"starts_with", []limen.Where{{Column: "key", Operator: limen.OpStartsWith, Value: "a"}}, []string{"r0", "r3", "r4"}},
		{"ends_with", []limen.Where{{Column: "key", Operator: limen.OpEndsWith, Value: "ta"}}, []string{"r1"}},
		{"or group", []limen.Where{limen.Eq("key", "alpha"), limen.Eq("key", "beta").Or(), {Column: "count", Operator: limen.OpGt, Value: 0}}, []string{"r1"}},
		{"time compare", []limen.Where{{Column: "last_request_at", Operator: limen.OpGte, Value: time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC).UnixMilli()}}, []string{"r3", "r4"}},
	}
	for _, c := range cases {
		rows, err := a.FindMany(ctx, "rate_limits", c.where, asc)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := ids(rows); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, got, c.want)
		}
	}
	// null handling
	create(t, a, "accounts", map[string]any{"id": "a1", "user_id": "u", "provider": "x", "provider_account_id": nil, "access_token": "t", "scope": ""})
	create(t, a, "accounts", map[string]any{"id": "a2", "user_id": "u", "provider": "x", "provider_account_id": "p", "access_token": "t", "scope": ""})
	for op, want := range map[limen.Operator]string{limen.OpIsNull: "a1", limen.OpIsNotNull: "a2"} {
		rows, err := a.FindMany(ctx, "accounts", []limen.Where{{Column: "provider_account_id", Operator: op}}, nil)
		if err != nil || len(rows) != 1 || rows[0]["id"] != want {
			t.Errorf("%s = %v %v", op, rows, err)
		}
	}
	if _, err := a.FindMany(ctx, "rate_limits", []limen.Where{{Column: "key", Operator: "bad"}}, nil); err == nil {
		t.Fatal("bad operator")
	}
}

func TestConformanceFindManyPaging(t *testing.T) {
	a := testAdapter(t)
	seedNumbers(t, a)
	ctx := context.Background()
	rows, err := a.FindMany(ctx, "rate_limits", nil, &limen.QueryOptions{OrderBy: []limen.OrderBy{{Column: "count", Direction: limen.OrderByDesc}}, Limit: 2, Offset: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(rows); !reflect.DeepEqual(got, []string{"r3", "r2"}) {
		t.Fatalf("paged = %v", got)
	}
	one, err := a.FindOne(ctx, "rate_limits", nil, []limen.OrderBy{{Column: "count", Direction: limen.OrderByDesc}})
	if err != nil || one["id"] != "r4" {
		t.Fatalf("findOne ordered = %v %v", one, err)
	}
	all, _ := a.FindMany(ctx, "rate_limits", nil, nil)
	if len(all) != 5 {
		t.Fatalf("all = %d", len(all))
	}
	// Limen's RateLimit model asserts these exact types
	if _, ok := one["count"].(int32); !ok {
		t.Fatalf("count type %T", one["count"])
	}
	if _, ok := one["last_request_at"].(int64); !ok {
		t.Fatalf("last_request_at type %T", one["last_request_at"])
	}
}

func TestConformanceUpdateDeleteCount(t *testing.T) {
	a := testAdapter(t)
	seedNumbers(t, a)
	ctx := context.Background()
	res, err := a.Update(ctx, "rate_limits", []limen.Where{{Column: "count", Operator: limen.OpGte, Value: 3}}, map[string]any{"last_request_at": int64(99)})
	if err != nil || res.RowsAffected != 2 {
		t.Fatalf("update = %v %v", res, err)
	}
	// RowsAffected counts matches, also when nothing changes (compare-and-swap semantics)
	res, _ = a.Update(ctx, "rate_limits", []limen.Where{limen.Eq("id", "r3")}, map[string]any{"last_request_at": int64(99)})
	if res.RowsAffected != 1 {
		t.Fatalf("unchanged update = %d", res.RowsAffected)
	}
	res, _ = a.Update(ctx, "rate_limits", []limen.Where{limen.Eq("id", "none")}, map[string]any{"last_request_at": int64(99)})
	if res.RowsAffected != 0 {
		t.Fatalf("no match = %d", res.RowsAffected)
	}
	if res, err := a.Update(ctx, "rate_limits", nil, map[string]any{}); err != nil || res.RowsAffected != 0 {
		t.Fatal("empty update")
	}
	res, err = a.Update(ctx, "rate_limits", []limen.Where{limen.Eq("id", "r1")}, map[string]any{"count": limen.IncrementBy(5), "last_request_at": limen.DecrementBy(1)})
	if err != nil || res.RowsAffected != 1 {
		t.Fatalf("arith = %v %v", res, err)
	}
	r1, _ := a.FindOne(ctx, "rate_limits", []limen.Where{limen.Eq("id", "r1")}, nil)
	if r1["count"] != int32(6) {
		t.Fatalf("count after inc = %#v", r1["count"])
	}
	if _, err := a.Update(ctx, "rate_limits", nil, map[string]any{"count": limen.IncrementBy(0)}); err == nil {
		t.Fatal("invalid arithmetic update")
	}
	if _, err := a.Update(ctx, "rate_limits", []limen.Where{{Column: "x", Operator: "bad"}}, map[string]any{"a": 1}); err == nil {
		t.Fatal("bad operator")
	}
	if _, err := a.Update(ctx, "custom", nil, map[string]any{"a": 1}); err != nil {
		t.Fatal(err)
	}
	n, err := a.Count(ctx, "rate_limits", []limen.Where{limen.Eq("last_request_at", 99)})
	if err != nil || n != 2 {
		t.Fatalf("count = %d %v", n, err)
	}
	ok, err := a.Exists(ctx, "rate_limits", []limen.Where{limen.Eq("last_request_at", 99)})
	if err != nil || !ok {
		t.Fatal("exists")
	}
	ok, _ = a.Exists(ctx, "rate_limits", []limen.Where{limen.Eq("key", "nope")})
	if ok {
		t.Fatal("exists miss")
	}
	del, err := a.Delete(ctx, "rate_limits", []limen.Where{limen.Eq("last_request_at", 99)})
	if err != nil || del.RowsAffected != 2 {
		t.Fatalf("delete = %v %v", del, err)
	}
	for _, bad := range []func() error{
		func() error { _, err := a.Delete(ctx, "x", []limen.Where{{Operator: "bad"}}); return err },
		func() error { _, err := a.Count(ctx, "x", []limen.Where{{Operator: "bad"}}); return err },
		func() error { _, err := a.Exists(ctx, "x", []limen.Where{{Operator: "bad"}}); return err },
		func() error { _, err := a.FindOne(ctx, "x", []limen.Where{{Operator: "bad"}}, nil); return err },
	} {
		if bad() == nil {
			t.Fatal("bad operator accepted")
		}
	}
}

func TestConformanceTypesRoundTrip(t *testing.T) {
	a := testAdapter(t)
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	maxReq, window := int32(10), int64(60000)
	create(t, a, "api_keys", map[string]any{"id": "k1", "name": "n", "profile": "default", "principal_type": "user", "principal_id": "u1",
		"key_hash": "h", "last4": "abcd", "enabled": true, "expires_at": &exp, "rate_limit_max": &maxReq, "rate_limit_window_ms": &window,
		"permissions": map[string]any{"accounts": []any{"read"}}, "metadata": map[string]any{"org": "o1"}})
	k, err := a.FindOne(ctx, "api_keys", []limen.Where{limen.Eq("id", "k1")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if k["rate_limit_max"] != int32(10) || k["rate_limit_window_ms"] != int64(60000) || k["enabled"] != true {
		t.Fatalf("typed = %#v %#v", k["rate_limit_max"], k["rate_limit_window_ms"])
	}
	if got, ok := k["expires_at"].(time.Time); !ok || !got.Equal(exp) {
		t.Fatalf("time = %#v", k["expires_at"])
	}
	if !reflect.DeepEqual(k["metadata"], map[string]any{"org": "o1"}) {
		t.Fatalf("map = %#v", k["metadata"])
	}
	if _, ok := k["created_at"].(time.Time); !ok {
		t.Fatal("created_at default")
	}
}

func TestConformanceTransactions(t *testing.T) {
	a := testAdapter(t)
	ctx := context.Background()
	create(t, a, "rate_limits", map[string]any{"id": "seed", "key": "seed", "count": 0, "last_request_at": int64(0)})

	tx, err := a.BeginTx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	create(t, tx, "rate_limits", map[string]any{"id": "t1", "key": "t1", "count": 1, "last_request_at": int64(0)})
	if _, err := tx.FindOne(ctx, "rate_limits", []limen.Where{limen.Eq("id", "t1")}, nil); err != nil {
		t.Fatalf("tx reads its own write: %v", err)
	}
	if _, err := a.FindOne(ctx, "rate_limits", []limen.Where{limen.Eq("id", "t1")}, nil); !errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatalf("uncommitted write visible outside: %v", err)
	}
	if _, err := tx.Update(ctx, "rate_limits", []limen.Where{limen.Eq("id", "seed")}, map[string]any{"count": limen.IncrementBy(1)}); err != nil {
		t.Fatal(err)
	}
	if n, err := tx.Count(ctx, "rate_limits", nil); err != nil || n != 2 {
		t.Fatalf("tx count = %d %v", n, err)
	}
	if ok, _ := tx.Exists(ctx, "rate_limits", []limen.Where{limen.Eq("id", "t1")}); !ok {
		t.Fatal("tx exists")
	}
	if rows, _ := tx.FindMany(ctx, "rate_limits", nil, nil); len(rows) != 2 {
		t.Fatal("tx find many")
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("double commit")
	}
	if err := tx.Rollback(); err == nil {
		t.Fatal("rollback after commit")
	}
	if _, err := a.FindOne(ctx, "rate_limits", []limen.Where{limen.Eq("id", "t1")}, nil); err != nil {
		t.Fatalf("committed write: %v", err)
	}

	tx, _ = a.BeginTx(ctx)
	create(t, tx, "rate_limits", map[string]any{"id": "t2", "key": "t2", "count": 1, "last_request_at": int64(0)})
	if _, err := tx.Delete(ctx, "rate_limits", []limen.Where{limen.Eq("id", "seed")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.FindOne(ctx, "rate_limits", []limen.Where{limen.Eq("id", "t2")}, nil); !errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatal("rolled-back write visible")
	}
	if _, err := a.FindOne(ctx, "rate_limits", []limen.Where{limen.Eq("id", "seed")}, nil); err != nil {
		t.Fatal("rolled-back delete applied")
	}
}

func TestConformanceIndexes(t *testing.T) {
	a := testAdapter(t)
	ctx := context.Background()
	if err := a.EnsureIndexes(ctx); err != nil {
		t.Fatalf("second EnsureIndexes: %v", err)
	}
	create(t, a, "users", map[string]any{"id": "u1", "email": "a@x.test"})
	if _, err := a.Create(ctx, "users", map[string]any{"id": "u2", "email": "a@x.test"}); err == nil || !mongo.IsDuplicateKeyError(errors.Unwrap(err)) && !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("duplicate email: %v", err)
	}
	// several NULL provider_account_id rows do not collide (SQL semantics); equal values do
	for _, id := range []string{"a1", "a2"} {
		create(t, a, "accounts", map[string]any{"id": id, "user_id": "u1", "provider": "p", "provider_account_id": nil, "access_token": "t", "scope": ""})
	}
	create(t, a, "accounts", map[string]any{"id": "a3", "user_id": "u1", "provider": "p", "provider_account_id": "x", "access_token": "t", "scope": ""})
	if _, err := a.Create(ctx, "accounts", map[string]any{"id": "a4", "user_id": "u1", "provider": "p", "provider_account_id": "x", "access_token": "t", "scope": ""}); err == nil {
		t.Fatal("duplicate provider account")
	}
	cur, err := a.Collection("sessions").Indexes().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var specs []bson.M
	if err := cur.All(ctx, &specs); err != nil {
		t.Fatal(err)
	}
	ttl := false
	for _, s := range specs {
		if s["name"] == "ttl_sessions_expires_at" {
			ttl = fmt.Sprint(s["expireAfterSeconds"]) == "0"
		}
	}
	if !ttl {
		t.Fatalf("no TTL index on auth_sessions: %v", specs)
	}
	// a broken database name makes EnsureIndexes fail with context
	bad, _ := New(a.db.Client().Database("bad$name"))
	if err := bad.EnsureIndexes(ctx); err == nil {
		t.Fatal("index creation on an invalid database must fail")
	}
}

func TestConformanceErrorsAfterDisconnect(t *testing.T) {
	a := testAdapter(t)
	ctx := context.Background()
	client, err := mongo.Connect(options.Client().ApplyURI(testURI()))
	if err != nil {
		t.Skip(err)
	}
	_ = client.Disconnect(ctx)
	b, _ := New(client.Database(a.db.Name()))
	if _, err := b.Create(ctx, "users", map[string]any{"email": "x"}); err == nil {
		t.Fatal("create on a disconnected client")
	}
	if _, err := b.FindOne(ctx, "users", nil, nil); err == nil || errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatal("find on a disconnected client")
	}
	if _, err := b.FindMany(ctx, "users", nil, nil); err == nil {
		t.Fatal("find many on a disconnected client")
	}
	if _, err := b.Update(ctx, "users", nil, map[string]any{"a": 1}); err == nil {
		t.Fatal("update on a disconnected client")
	}
	if _, err := b.Delete(ctx, "users", nil); err == nil {
		t.Fatal("delete on a disconnected client")
	}
	if _, err := b.Exists(ctx, "users", nil); err == nil {
		t.Fatal("exists on a disconnected client")
	}
	if _, err := b.Count(ctx, "users", nil); err == nil {
		t.Fatal("count on a disconnected client")
	}
	if b.Schema() == nil {
		t.Fatal("schema")
	}
}

func testURI() string {
	if uri := os.Getenv("EB_TEST_MONGO_URI"); uri != "" {
		return uri
	}
	return "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
}
