package events

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestNormalizeAndTypeFilter(t *testing.T) {
	got := normalizeTypes([]string{"message.received,account.added", " message.received ", "", "mailbox.*"})
	if len(got) != 3 || got[0] != "message.received" || got[1] != "account.added" || got[2] != "mailbox.*" {
		t.Fatalf("normalize: %#v", got)
	}
	f, err := typeFilter([]string{"message.received"})
	if err != nil || f["type"] != "message.received" {
		t.Fatalf("exact: %v %v", f, err)
	}
	f, err = typeFilter([]string{"message.received", "message.updated"})
	if err != nil {
		t.Fatal(err)
	}
	tm, ok := f["type"].(bson.M)
	if !ok {
		t.Fatalf("$in type: %#v", f)
	}
	in, ok := tm["$in"].([]string)
	if !ok || len(in) != 2 {
		t.Fatalf("$in: %#v", f)
	}
	f, err = typeFilter([]string{"message.*"})
	if err != nil {
		t.Fatal(err)
	}
	rx, ok := f["type"].(bson.M)["$regex"].(string)
	if !ok || rx != "^message\\." {
		t.Fatalf("wildcard: %#v", f)
	}
	f, err = typeFilter([]string{"message.*", "account.added"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f["$or"]; !ok {
		t.Fatalf("mixed: %#v", f)
	}
	if _, err := typeFilter([]string{"message.*.extra"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad wildcard: %v", err)
	}
	if _, err := typeFilter([]string{"*"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("star: %v", err)
	}
	if f, err := typeFilter(nil); err != nil || f != nil {
		t.Fatal("empty")
	}
}

func TestMatchType(t *testing.T) {
	if !matchType("message.received", nil) || !matchType("message.received", []string{}) {
		t.Fatal("no filter")
	}
	if !matchType("message.received", []string{"message.received"}) {
		t.Fatal("exact")
	}
	if matchType("message.updated", []string{"message.received"}) {
		t.Fatal("miss")
	}
	if !matchType("message.updated", []string{"message.*"}) {
		t.Fatal("family")
	}
	if matchType("account.added", []string{"message.*"}) {
		t.Fatal("other family")
	}
	if !matchType("account.added", []string{"message.*", "account.added"}) {
		t.Fatal("mixed")
	}
}

func TestEventSortAndCursor(t *testing.T) {
	field, dir, err := eventSort("")
	if err != nil || field != "time" || dir != -1 {
		t.Fatalf("default: %s %d %v", field, dir, err)
	}
	field, dir, err = eventSort("time")
	if err != nil || field != "time" || dir != 1 {
		t.Fatalf("asc: %s %d %v", field, dir, err)
	}
	field, dir, err = eventSort("-id")
	if err != nil || field != "_id" || dir != -1 {
		t.Fatalf("id: %s %d %v", field, dir, err)
	}
	if _, _, err := eventSort("-createdAt"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatalf("bad sort: %v", err)
	}
	tok := encodeListCursor(listCursor{ID: "evt_1", Key: time.Now().UTC().Format(time.RFC3339Nano), Field: "time", Desc: true})
	c, err := decodeListCursor(tok)
	if err != nil || c.ID != "evt_1" || c.Field != "time" || !c.Desc {
		t.Fatalf("cursor: %+v %v", c, err)
	}
	if _, err := decodeListCursor("%%%"); err == nil {
		t.Fatal("bad cursor")
	}
	if listLimit(0) != defaultListLimit || listLimit(999) != maxListLimit || listLimit(10) != 10 {
		t.Fatal("limit")
	}
}

func TestListMatch(t *testing.T) {
	acc := store.NewID(store.PrefixAccount)
	from := time.Now().Add(-time.Hour)
	to := time.Now()
	m, err := listMatch(ListFilter{AccountID: acc, Type: []string{"message.*"}, From: from, To: to})
	if err != nil || m["accountId"] != acc {
		t.Fatalf("%v %v", m, err)
	}
	rng := m["time"].(bson.M)
	if rng["$gte"] != from.UTC() || rng["$lte"] != to.UTC() {
		t.Fatalf("range: %#v", rng)
	}
	if _, err := listMatch(ListFilter{AccountID: "bad"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad account: %v", err)
	}
}

func TestStreamLimiter(t *testing.T) {
	l := newStreamLimiter(2)
	if !l.acquire("o1") {
		t.Fatal("first")
	}
	if !l.acquire("o1") {
		t.Fatal("second")
	}
	if l.acquire("o1") {
		t.Fatal("cap")
	}
	if !l.acquire("o2") {
		t.Fatal("other org")
	}
	l.release("o1")
	if !l.acquire("o1") {
		t.Fatal("after release")
	}
	l.release("o1")
	l.release("o1")
	l.release("o1") // extra release must not panic
}

func TestTrimEmitted(t *testing.T) {
	m := map[string]struct{}{"a": {}, "b": {}, "c": {}}
	out := trimEmitted(m, 2)
	if len(out) != 2 {
		t.Fatalf("%d", len(out))
	}
	if _, ok := out["a"]; ok { // the oldest ID goes, the newest stay
		t.Fatalf("kept the oldest id: %v", out)
	}
	if len(trimEmitted(m, 10)) != 3 {
		t.Fatal("keep all")
	}
}

func TestRegexpQuote(t *testing.T) {
	if regexpQuote("a.b") != `a\.b` {
		t.Fatal(regexpQuote("a.b"))
	}
}
