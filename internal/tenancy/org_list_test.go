package tenancy

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestApplyOrgSearch(t *testing.T) {
	base := bson.M{}
	if err := applyOrgSearch(base, ""); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(base) != 0 {
		t.Fatalf("empty mutates: %#v", base)
	}

	base = bson.M{}
	if err := applyOrgSearch(base, "  Acme  "); err != nil {
		t.Fatalf("ok: %v", err)
	}
	or, ok := base["$or"].([]bson.M)
	if !ok || len(or) != 2 {
		t.Fatalf("$or: %#v", base["$or"])
	}
	for _, branch := range or {
		var rx bson.M
		if n, ok := branch["name"].(bson.M); ok {
			rx = n
		} else if s, ok := branch["slug"].(bson.M); ok {
			rx = s
		} else {
			t.Fatalf("branch: %#v", branch)
		}
		if rx["$regex"] != "Acme" || rx["$options"] != "i" {
			t.Fatalf("rx: %#v", rx)
		}
	}

	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	if err := applyOrgSearch(bson.M{}, string(long)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long q: %v", err)
	}
}

func TestAttachOrgCountsMaps(t *testing.T) {
	rows := []Organization{
		{ID: "org_a"},
		{ID: "org_b"},
		{ID: "org_c"},
	}
	members := map[string]int64{"org_a": 3, "org_b": 1}
	accounts := map[string]int64{"org_a": 7}
	for i := range rows {
		rows[i].MemberCount = members[rows[i].ID]
		rows[i].AccountCount = accounts[rows[i].ID]
	}
	if rows[0].MemberCount != 3 || rows[0].AccountCount != 7 {
		t.Fatalf("a: %+v", rows[0])
	}
	if rows[1].MemberCount != 1 || rows[1].AccountCount != 0 {
		t.Fatalf("b: %+v", rows[1])
	}
	if rows[2].MemberCount != 0 || rows[2].AccountCount != 0 {
		t.Fatalf("c: %+v", rows[2])
	}
}

func TestCloneFilter(t *testing.T) {
	in := bson.M{"a": 1}
	out := cloneFilter(in)
	out["b"] = 2
	if _, ok := in["b"]; ok {
		t.Fatal("clone shared map")
	}
}
