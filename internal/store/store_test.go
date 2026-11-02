package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestNewIDFormatAndOrder(t *testing.T) {
	a := NewID(PrefixAccount)
	b := NewID(PrefixAccount)
	if !strings.HasPrefix(a, "acc_") || len(a) != 4+26 || a != strings.ToLower(a) {
		t.Fatalf("bad id %q", a)
	}
	if a >= b {
		t.Fatalf("ids not increasing: %s >= %s", a, b)
	}
	if !IsID(PrefixAccount, a) || IsID(PrefixEvent, a) {
		t.Fatal("IsID wrong")
	}
	ts, err := IDTime(a)
	if err != nil || time.Since(ts) > time.Minute {
		t.Fatalf("IDTime = %v %v", ts, err)
	}
}

func TestNewIDMonotonicWithinMillisecond(t *testing.T) {
	now := time.Now()
	prev := ""
	for i := 0; i < 1000; i++ {
		id := newIDAt(PrefixEvent, now)
		if id <= prev {
			t.Fatalf("not monotonic at %d: %s <= %s", i, id, prev)
		}
		prev = id
	}
}

func TestNewIDUnknownPrefixPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	NewID("zzz")
}

func TestParseIDRejects(t *testing.T) {
	for _, s := range []string{"", "acc", "zzz_01jbf3k8v4w7q2m9x5c6t0r1yz", "acc_01JBF3K8V4W7Q2M9X5C6T0R1YZ", "acc_short", "acc_01jbf3k8v4w7q2m9x5c6t0r1y!"} {
		if _, _, err := ParseID(s); err == nil {
			t.Errorf("ParseID(%q) accepted", s)
		}
	}
	if !KnownPrefix(PrefixDataKey) || KnownPrefix("nope") {
		t.Fatal("KnownPrefix wrong")
	}
}

func TestScoped(t *testing.T) {
	if _, err := Scoped(context.Background(), bson.M{}); !errors.Is(err, ErrNoTenant) {
		t.Fatalf("want ErrNoTenant, got %v", err)
	}
	ctx := WithOrganization(context.Background(), "org_a")
	f, err := Scoped(ctx, bson.M{"state": "connected"})
	if err != nil || f["organizationId"] != "org_a" || f["state"] != "connected" {
		t.Fatalf("Scoped = %v %v", f, err)
	}
	orig := bson.M{"x": 1}
	_, _ = Scoped(ctx, orig)
	if _, has := orig["organizationId"]; has {
		t.Fatal("Scoped must not mutate the input filter")
	}
	if _, err := Scoped(ctx, bson.M{"organizationId": "org_b"}); err == nil {
		t.Fatal("conflicting organisation must be rejected")
	}
	if _, ok := OrganizationFrom(WithOrganization(context.Background(), "")); ok {
		t.Fatal("empty org must not count")
	}
}

func noop(context.Context, *DB) error { return nil }

func TestMigratorValidate(t *testing.T) {
	tests := []struct {
		name string
		ms   []Migration
		want string
	}{
		{"zero", []Migration{{Number: 0, Name: "a", Up: noop}}, "must be > 0"},
		{"dup", []Migration{{Number: 1, Name: "a", Up: noop}, {Number: 1, Name: "b", Up: noop}}, "used twice"},
		{"order", []Migration{{Number: 2, Name: "a", Up: noop}, {Number: 1, Name: "b", Up: noop}}, "ascending"},
		{"noup", []Migration{{Number: 1, Name: "a"}}, "needs a name"},
		{"ok", []Migration{{Number: 1, Name: "a", Up: noop}, {Number: 3, Name: "b", Up: noop}}, ""},
	}
	for _, tt := range tests {
		err := (&Migrator{Migrations: tt.ms}).validate()
		if tt.want == "" && err != nil || tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
			t.Errorf("%s: err = %v, want %q", tt.name, err, tt.want)
		}
	}
	if (Migration{Number: 1, Name: "a"}).checksum() == (Migration{Number: 1, Name: "b"}).checksum() {
		t.Fatal("checksum must depend on the name")
	}
}

func TestConnectValidation(t *testing.T) {
	if _, err := Connect(context.Background(), Options{}); err == nil {
		t.Fatal("empty options must fail")
	}
	m := &Module{}
	if m.Name() != "store" || m.Roles() != nil {
		t.Fatal("module identity")
	}
	if err := m.Ready(context.Background()); !errors.Is(err, ErrNotConnected) || err.Error() == "" {
		t.Fatalf("Ready before start = %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
