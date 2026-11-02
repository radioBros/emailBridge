package sync

import "testing"

func TestMergeIMAPID(t *testing.T) {
	global := map[string]string{"name": "EmailBridge", "version": "1.0", "vendor": "EB"}
	acc := map[string]string{"name": "AcmeMail", "support-url": "https://acme.example/support"}
	got := MergeIMAPID(global, acc)
	if got["name"] != "AcmeMail" || got["version"] != "1.0" || got["vendor"] != "EB" || got["support-url"] != "https://acme.example/support" {
		t.Fatalf("%v", got)
	}
	global["name"] = "mutated"
	if got["name"] != "AcmeMail" {
		t.Fatal("mutated global leaked")
	}
}

func TestAccountIMAPID(t *testing.T) {
	if AccountIMAPID("", "", "", "") != nil {
		t.Fatal("expected nil")
	}
	m := AccountIMAPID("N", "V", "Vend", "https://x")
	if m["name"] != "N" || m["version"] != "V" || m["vendor"] != "Vend" || m["support-url"] != "https://x" {
		t.Fatalf("%v", m)
	}
}

func TestMergeIMAPIDEmpty(t *testing.T) {
	if MergeIMAPID(nil, nil) != nil {
		t.Fatal()
	}
}
