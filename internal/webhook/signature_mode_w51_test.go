package webhook

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/features"
)

func TestSignatureModeResolveLayers(t *testing.T) {
	t.Parallel()
	svc := &Service{}
	if got := svc.signatureMode(context.Background(), nil); got != SignatureStandard {
		t.Fatalf("nil endpoint=%s", got)
	}
	ep := &Endpoint{
		OrganizationID: "org_01jbf3k8v4w7q2m9x5c6t0r1yz",
		Security:       SecurityOptions{SignatureMode: SignatureStandard},
	}
	if got := svc.signatureMode(context.Background(), ep); got != SignatureStandard {
		t.Fatalf("default=%s", got)
	}
	ep.Security.SignatureMode = SignatureBodyOnly
	if got := svc.signatureMode(context.Background(), ep); got != SignatureBodyOnly {
		t.Fatalf("security fallback=%s", got)
	}
	ep.Security.SignatureMode = SignatureStandard
	ep.Switches = map[string]any{"webhook.signature.mode": SignatureBodyOnly}
	if got := svc.signatureMode(context.Background(), ep); got != SignatureBodyOnly {
		t.Fatalf("endpoint switch=%s", got)
	}
	if got := fallbackSignatureMode(nil); got != SignatureStandard {
		t.Fatal(got)
	}
	if got := fallbackSignatureMode(&Endpoint{Switches: map[string]any{"webhook.signature.mode": SignatureBodyOnly}}); got != SignatureBodyOnly {
		t.Fatal(got)
	}

	// Org value wins when endpoint switch unset (no Mongo — inject via Resolve unit path).
	sw, _ := features.DefaultRegistry().Get("webhook.signature.mode")
	v, src := features.Resolve(sw, "production", features.Values{
		Organization: &features.Setting{Value: features.StringValue(SignatureBodyOnly)},
	})
	if src != features.SourceOrganization || v.String == nil || *v.String != SignatureBodyOnly {
		t.Fatalf("org resolve %v %s", v, src)
	}
	v, src = features.Resolve(sw, "production", features.Values{
		Organization: &features.Setting{Value: features.StringValue(SignatureBodyOnly)},
		Lower:        &features.Setting{Value: features.StringValue(SignatureStandard)},
	})
	if src != features.SourceLower || *v.String != SignatureStandard {
		t.Fatalf("lower wins unlocked %v %s", v, src)
	}
	v, src = features.Resolve(sw, "production", features.Values{
		Organization: &features.Setting{Value: features.StringValue(SignatureBodyOnly), Locked: true},
		Lower:        &features.Setting{Value: features.StringValue(SignatureStandard)},
	})
	// signature.mode is not Lockable — lock ignored; lower wins.
	if sw.Lockable {
		t.Fatal("signature.mode should not be lockable")
	}
	if src != features.SourceLower {
		t.Fatalf("non-lockable org locked: %s", src)
	}
}
