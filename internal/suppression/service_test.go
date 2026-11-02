package suppression

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestNormalizeAndValidate(t *testing.T) {
	a, err := normalizeAddress(" Ada <Ada@Example.TEST> ")
	if err != nil || a != "ada@example.test" {
		t.Fatalf("%q %v", a, err)
	}
	if _, err := normalizeAddress("not-an-email"); err == nil {
		t.Fatal("bad addr")
	}
	if _, err := validateName(""); err == nil {
		t.Fatal("name")
	}
	if _, err := validateName(strings.Repeat("n", 200)); err == nil {
		t.Fatal("long name")
	}
	if !validKind(KindManual) || validKind("nope") {
		t.Fatal("kind")
	}
	if err := checkIfMatch(`"v2"`, 1); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if formatETag(3) != `"v3"` {
		t.Fatal(formatETag(3))
	}
	if systemName(KindUnsubscribe) != "Unsubscribes" {
		t.Fatal(systemName(KindUnsubscribe))
	}
	u := uniqueNorm([]string{"A@B.TEST", "a@b.test", "bad"})
	if len(u) != 1 || u[0] != "a@b.test" {
		t.Fatalf("%v", u)
	}
}

func TestEntryID(t *testing.T) {
	if entryID("sup_1", "a@b.test") != "sup_1:a@b.test" {
		t.Fatal(entryID("sup_1", "a@b.test"))
	}
	_ = time.Now()
}
