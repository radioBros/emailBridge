package webhook

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
)

func TestSignatureModeUsesSettingsRegistry(t *testing.T) {
	t.Parallel()
	reg := features.NewRegistry()
	if err := reg.Register(features.Switch{
		Name: "webhook.signature.mode", Scopes: []features.Scope{features.ScopeGlobal, features.ScopeOrganization, features.ScopeEndpoint},
		Default: features.StringValue(SignatureStandard),
	}); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Settings: &settings.Service{Registry: reg, EnvName: "staging"}}
	ep := &Endpoint{Security: SecurityOptions{SignatureMode: SignatureStandard}}
	if got := svc.signatureMode(context.Background(), ep); got != SignatureStandard {
		t.Fatalf("got %s", got)
	}
}
