package webhook

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestUnionIDsAndCloneMap(t *testing.T) {
	t.Parallel()
	got := unionIDs([]string{"a", "b", ""}, []string{"b", "c"})
	if len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("%v", got)
	}
	in := map[string]any{"x": 1}
	out := cloneAnyMap(in)
	out["x"] = 2
	if in["x"] != 1 || out["x"] != 2 {
		t.Fatal("clone")
	}
	if fallbackSignatureMode(&Endpoint{}) != SignatureStandard {
		t.Fatal("empty endpoint fallback")
	}
	m := ReplayedAtIndexMigration(19)
	if m.Number != 19 || m.Up == nil || m.Name == "" {
		t.Fatalf("%+v", m)
	}
	if errorsNewPublisher() == nil {
		t.Fatal("publisher err")
	}
	_ = errs.CodeInternalError
}
