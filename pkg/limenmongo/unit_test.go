package limenmongo

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/thecodearcher/limen"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type named string

func mustSchema(t *testing.T) *Schema {
	t.Helper()
	s, err := DefaultSchema()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestEmbeddedSchemaHasEveryTable(t *testing.T) {
	s := mustSchema(t)
	want := []string{"accounts", "api_keys", "passkeys", "rate_limits", "sessions", "two_factors", "users", "verifications"}
	if got := s.TableNames(); !reflect.DeepEqual(got, want) {
		t.Fatalf("tables = %v", got)
	}
	u := s.Table("users")
	c, ok := u.Column("two_factor_enabled")
	if !ok || c.Type != typeBool || c.DefaultValue != "false" {
		t.Fatalf("users.two_factor_enabled = %+v", c)
	}
	if s.Table("nope") != nil || (*Schema)(nil).Table("users") != nil {
		t.Fatal("unknown table")
	}
	if len(EmbeddedSchemaJSON()) == 0 {
		t.Fatal("embedded file")
	}
}

func TestParseSchemaErrors(t *testing.T) {
	for _, raw := range []string{`not json`, `{"schemas":{}}`,
		`{"schemas":{"t":{"Columns":[{"Name":"n","Type":"bool","DefaultValue":"maybe"}]}}}`,
		`{"schemas":{"t":{"Columns":[{"Name":"n","Type":"int","DefaultValue":"x"}]}}}`,
		`{"schemas":{"t":{"Columns":[{"Name":"n","Type":"int64","DefaultValue":"x"}]}}}`,
		`{"schemas":{"t":{"Columns":[{"Name":"n","Type":"time.Time","DefaultValue":"x"}]}}}`,
	} {
		if _, err := ParseSchema([]byte(raw)); err == nil {
			t.Errorf("ParseSchema(%s) succeeded", raw)
		}
	}
	s, err := ParseSchema([]byte(`{"schemas":{"t":{"Columns":[{"Name":"n","Type":"string"}]},"nil":null}}`))
	if err != nil || s.Table("t") == nil {
		t.Fatalf("table name from key: %v", err)
	}
}

func TestParseDefault(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("x", 3600))
	cases := []struct {
		c    Column
		want any
	}{
		{Column{Type: typeTime, DefaultValue: NowSentinel}, now.UTC()},
		{Column{Type: typeBool, DefaultValue: "true"}, true},
		{Column{Type: typeInt, DefaultValue: "7"}, int32(7)},
		{Column{Type: typeInt64, DefaultValue: "0"}, int64(0)},
		{Column{Type: typeTime, DefaultValue: "2026-01-01T00:00:00Z"}, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Column{Type: typeString, DefaultValue: "x"}, "x"},
		{Column{Type: typeString}, nil},
	}
	for _, c := range cases {
		got, err := parseDefault(c.c, now)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseDefault(%+v) = %#v, %v", c.c, got, err)
		}
	}
}

func TestCoerceAndNormalize(t *testing.T) {
	s := "x"
	tm := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("x", 7200))
	var nilStr *string
	cases := []struct {
		typ  string
		in   any
		want any
	}{
		{typeInt, int64(5), int32(5)},
		{typeInt, 5, int32(5)},
		{typeInt32, uint8(3), int32(3)},
		{typeInt, int64(1) << 40, int64(1) << 40}, // does not fit: unchanged
		{typeInt64, int32(9), int64(9)},
		{typeInt64, float64(2), int64(2)},
		{typeInt64, 2.5, 2.5},
		{typeInt64, uint64(1) << 63, uint64(1) << 63},
		{typeTime, bson.NewDateTimeFromTime(tm), tm.UTC()},
		{typeTime, &tm, tm.UTC()},
		{typeMap, bson.D{{Key: "a", Value: int32(1)}}, map[string]any{"a": int32(1)}},
		{typeMap, "json", "json"},
		{typeString, &s, "x"},
		{typeString, nilStr, nil},
		{typeString, named("n"), "n"},
		{typeBool, true, true},
		{"", []byte("b"), []byte("b")},
	}
	for _, c := range cases {
		if got := coerce(c.typ, c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("coerce(%s, %#v) = %#v, want %#v", c.typ, c.in, got, c.want)
		}
	}
	oid := bson.NewObjectID()
	got := normalize(bson.M{"a": bson.A{bson.D{{Key: "b", Value: bson.Binary{Data: []byte{1}}}}}, "o": oid, "m": map[string]any{"t": tm}})
	want := map[string]any{"a": []any{map[string]any{"b": []byte{1}}}, "o": oid.Hex(), "m": map[string]any{"t": tm.UTC()}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalize = %#v", got)
	}
	if _, ok := toArray("abc"); ok {
		t.Fatal("string is not a list")
	}
	if _, ok := toArray([]byte("abc")); ok {
		t.Fatal("bytes are not a list")
	}
	if _, ok := toArray(nil); ok {
		t.Fatal("nil is not a list")
	}
	if a, ok := toArray([2]named{"a", "b"}); !ok || !reflect.DeepEqual(a, bson.A{"a", "b"}) {
		t.Fatalf("toArray = %v", a)
	}
}

func TestBuildFilter(t *testing.T) {
	tbl := mustSchema(t).Table("rate_limits")
	f, err := buildFilter(nil, nil)
	if err != nil || len(f) != 0 {
		t.Fatal("empty filter")
	}
	f, err = buildFilter(tbl, []limen.Where{limen.Eq("id", "a")})
	if err != nil || !reflect.DeepEqual(f, bson.M{"_id": bson.M{"$eq": "a"}}) {
		t.Fatalf("eq = %v %v", f, err)
	}
	// typed by column: count is an int column
	f, _ = buildFilter(tbl, []limen.Where{{Column: "count", Operator: limen.OpGt, Value: 3}})
	if !reflect.DeepEqual(f, bson.M{"count": bson.M{"$gt": int32(3)}}) {
		t.Fatalf("typed = %v", f)
	}
	f, _ = buildFilter(nil, []limen.Where{limen.Eq("a", 1), limen.Eq("b", 2).Or(), limen.Eq("c", 3)})
	want := bson.M{"$and": bson.A{
		bson.M{"$or": bson.A{bson.M{"a": bson.M{"$eq": 1}}, bson.M{"b": bson.M{"$eq": 2}}}},
		bson.M{"c": bson.M{"$eq": 3}},
	}}
	if !reflect.DeepEqual(f, want) {
		t.Fatalf("or groups = %v", f)
	}
	f, _ = buildFilter(nil, []limen.Where{{Column: "s", Operator: limen.OpContains, Value: "a.b*"}})
	if f["s"].(bson.M)["$regex"] != `a\.b\*` {
		t.Fatalf("escaped regex = %v", f)
	}
	f, _ = buildFilter(nil, []limen.Where{{Column: "s", Operator: limen.OpIn, Value: "notalist"}})
	if !reflect.DeepEqual(f, bson.M{"s": bson.M{"$in": bson.A{}}}) {
		t.Fatalf("in non-list = %v", f)
	}
	f, _ = buildFilter(nil, []limen.Where{{Column: "s", Operator: limen.OpNotIn, Value: 5}})
	if len(f) != 0 {
		t.Fatalf("not_in non-list = %v", f)
	}
	if _, err := buildFilter(nil, []limen.Where{{Column: "s", Operator: "bogus"}}); err == nil {
		t.Fatal("unknown operator")
	}
	if _, err := buildFilter(nil, []limen.Where{limen.Eq("a", 1), {Column: "s", Operator: "bogus", Connector: limen.ConnectorOr}}); err == nil {
		t.Fatal("unknown operator in or group")
	}
	if _, err := buildFilter(nil, []limen.Where{{Column: "s", Operator: "bogus"}, limen.Eq("a", 1)}); err == nil {
		t.Fatal("unknown operator in and group")
	}
	s := buildSort([]limen.OrderBy{{Column: "id", Direction: limen.OrderByDesc}, {Column: ""}, {Column: "a"}})
	if !reflect.DeepEqual(s, bson.D{{Key: "_id", Value: -1}, {Key: "a", Value: 1}}) {
		t.Fatalf("sort = %v", s)
	}
}

func TestIndexModels(t *testing.T) {
	a := &Adapter{prefix: "auth_", schema: mustSchema(t), ttlColumns: map[string]string{"sessions": "expires_at", "verifications": "expires_at"}}
	models := a.IndexModels()
	find := func(coll, name string) *options.IndexOptions {
		for _, m := range models[coll] {
			o := &options.IndexOptions{}
			for _, set := range m.Options.List() {
				_ = set(o)
			}
			if o.Name != nil && *o.Name == name {
				return o
			}
		}
		return nil
	}
	if o := find("auth_users", "idx_users_email"); o == nil || o.Unique == nil || !*o.Unique || o.PartialFilterExpression != nil {
		t.Fatalf("users.email index = %+v", o)
	}
	o := find("auth_accounts", "idx_accounts_provider_provider_account_id")
	if o == nil || o.PartialFilterExpression == nil {
		t.Fatalf("nullable unique index needs a partial filter: %+v", o)
	}
	for _, coll := range []string{"sessions", "verifications"} {
		o := find("auth_"+coll, "ttl_"+coll+"_expires_at")
		if o == nil || o.ExpireAfterSeconds == nil || *o.ExpireAfterSeconds != 0 {
			t.Fatalf("%s ttl index = %+v", coll, o)
		}
	}
	// a schema index on the TTL column itself becomes the TTL index
	s, err := ParseSchema([]byte(`{"schemas":{"x":{"Columns":[{"Name":"exp","Type":"time.Time","IsNullable":true},{"Name":"k","Type":"int","IsNullable":true}],
		"Indexes":[{"Name":"idx_x_exp","Columns":["exp"]},{"Name":"idx_x_k","Columns":["k"],"Unique":true},{"Name":"empty"}]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	a = &Adapter{prefix: "p_", schema: s, ttlColumns: map[string]string{"x": "exp"}}
	models = a.IndexModels()
	if len(models["p_x"]) != 2 {
		t.Fatalf("models = %d", len(models["p_x"]))
	}
	if o := find("p_x", "idx_x_exp"); o == nil || o.ExpireAfterSeconds == nil {
		t.Fatal("schema index not turned into ttl")
	}
	if o := find("p_x", "idx_x_k"); o == nil || !strings.Contains(fmtAny(o.PartialFilterExpression), "long") {
		t.Fatalf("int partial = %+v", o)
	}
	for _, typ := range []string{typeBool, typeTime, typeMap, typeText} {
		if len(bsonTypes(typ)) == 0 {
			t.Fatal(typ)
		}
	}
}

func fmtAny(v any) string { return strings.TrimSpace(strings.ReplaceAll(bsonString(v), "\n", "")) }

func bsonString(v any) string {
	b, err := bson.MarshalExtJSON(bson.M{"v": v}, false, false)
	if err != nil {
		return ""
	}
	return string(b)
}

func TestNewRequiresDatabase(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("nil database")
	}
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	s := mustSchema(t)
	a, err := New(client.Database("x"), WithSchema(s), WithPrefix("p_"), WithTTLColumns(map[string]string{}))
	if err != nil || a.Schema() != s || a.Collection("users").Name() != "p_users" || len(a.IndexModels()["p_sessions"]) != 2 {
		t.Fatalf("options not applied: %v", err)
	}
	if fieldName("id") != "_id" || fieldName("x") != "x" {
		t.Fatal("fieldName")
	}
}
