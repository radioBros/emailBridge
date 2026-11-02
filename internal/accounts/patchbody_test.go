package accounts

import (
	"encoding/json"
	"testing"
)

// PATCH /v1/accounts/{id} carries oauth through to the service (W43): the HTTP body type once dropped it.
func TestPatchBodyCarriesOAuth(t *testing.T) {
	var b patchBody
	if err := json.Unmarshal([]byte(`{"oauth":{"appId":"oap_x"},"name":"n"}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.OAuth == nil || b.OAuth.AppID != "oap_x" || b.Name == nil {
		t.Fatalf("patch body: %+v", b)
	}
}

func TestPatchBodyCarriesW76Fields(t *testing.T) {
	raw := `{"network":{"ehloName":"mx"},"auth":{"useAuthServer":true},"rePin":true,"provider":"imap","delegated":{"account":"acc_x","user":"u@ex.com"}}`
	var b patchBody
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatal(err)
	}
	if b.Network == nil || b.Network.EHLOName != "mx" || b.Auth == nil || !b.Auth.UseAuthServer || !b.RePin {
		t.Fatalf("%+v", b)
	}
	if b.Provider == nil || *b.Provider != "imap" || len(b.Delegated) == 0 {
		t.Fatalf("%+v", b)
	}
	var clear patchBody
	if err := json.Unmarshal([]byte(`{"delegated":null}`), &clear); err != nil {
		t.Fatal(err)
	}
	if string(clear.Delegated) != "null" {
		t.Fatalf("%q", clear.Delegated)
	}
}
