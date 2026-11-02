package audit

import "testing"

func TestDiffStructsAndRedaction(t *testing.T) {
	type acc struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Token    string `json:"apiToken"`
	}
	before := acc{Name: "a", Password: "secret1", Token: "tok1"}
	after := acc{Name: "b", Password: "secret2", Token: "tok2"}
	ch := Diff(before, after)
	if ch["name"].Before != "a" || ch["name"].After != "b" {
		t.Fatalf("name: %+v", ch["name"])
	}
	if ch["password"].Before != redacted || ch["password"].After != redacted {
		t.Fatalf("password not redacted: %+v", ch["password"])
	}
	if ch["apiToken"].Before != redacted || ch["apiToken"].After != redacted {
		t.Fatalf("apiToken not redacted: %+v", ch["apiToken"])
	}
}

func TestDiffSecretTypeAndDenyList(t *testing.T) {
	before := map[string]any{"dsn": "postgres://x", "note": "hi"}
	after := map[string]any{"dsn": "postgres://y", "note": "bye", "extra": Secret("x")}
	ch := Diff(before, after, "note")
	if ch["dsn"].Before != redacted || ch["dsn"].After != redacted {
		t.Fatalf("dsn: %+v", ch["dsn"])
	}
	if ch["note"].Before != redacted || ch["note"].After != redacted {
		t.Fatalf("note deny-list: %+v", ch["note"])
	}
	if ch["extra"].After != redacted {
		t.Fatalf("Secret type: %+v", ch["extra"])
	}
}

func TestDiffUnchanged(t *testing.T) {
	if Diff(map[string]any{"a": 1}, map[string]any{"a": 1}) != nil {
		t.Fatal("expected nil changes")
	}
}

func TestDiffMaps(t *testing.T) {
	ch := Diff(map[string]any{"x": 1}, map[string]any{"x": 2, "y": 3})
	if ch["x"].Before != float64(1) && ch["x"].Before != 1 {
		// json round-trip uses float64 for numbers when via struct; maps keep int
		if ch["x"].Before != 1 {
			t.Fatalf("x before: %#v", ch["x"].Before)
		}
	}
	if ch["y"].After != 3 {
		t.Fatalf("y: %+v", ch["y"])
	}
}
