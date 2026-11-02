package webhook

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestMappedBodySealOpenWithoutCipher(t *testing.T) {
	s := &Service{}
	d := &Delivery{ID: "dlv_1", OrganizationID: "org_1", MappedBody: "stale"}
	if err := s.sealMappedBody(context.Background(), d, ""); err != nil || d.MappedBody != "" || !d.MappedBodyEnc.IsZero() {
		t.Fatalf("empty: %+v %v", d, err)
	}
	if err := s.sealMappedBody(context.Background(), d, `{"k":1}`); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no cipher must fail closed (never store plaintext): %v", err)
	}
	if d.MappedBody != "" {
		t.Fatal("plaintext kept")
	}
	if body, ok, err := s.openMappedBody(context.Background(), &Delivery{MappedBody: `{"legacy":1}`}); err != nil || !ok || body != `{"legacy":1}` {
		t.Fatalf("legacy read: %q %v %v", body, ok, err)
	}
	if _, ok, err := s.openMappedBody(context.Background(), &Delivery{}); ok || err != nil {
		t.Fatal("no body")
	}
	enc := &Delivery{MappedBodyEnc: crypto.EncryptedString{DEK: "dek_1", CT: []byte{1}}}
	if _, _, err := s.openMappedBody(context.Background(), enc); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no cipher on read: %v", err)
	}
}

func TestDeliveryJSONNeverIncludesMappedBody(t *testing.T) {
	d := Delivery{ID: "dlv_1", MappedBody: "secret-plain", MappedBodyEnc: crypto.EncryptedString{DEK: "dek", CT: []byte("x")}}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	for _, k := range []string{"mappedBody", "MappedBody", "mappedBodyEnc", "MappedBodyEnc"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s exposed in JSON: %s", k, b)
		}
	}
}
