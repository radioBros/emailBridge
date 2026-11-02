package integrations

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/integrations/connectors"
)

func TestMaterializeSentryOwnerOnly(t *testing.T) {
	def := Materialize(connectors.Sentry())
	if def == nil || !def.InstanceOwnerOnly || def.ID != connectors.TypeSentry {
		t.Fatalf("%+v", def)
	}
	for _, raw := range []connectors.Def{
		connectors.SSOAuthentik(), connectors.SSOKeycloak(), connectors.SSOEntra(),
		connectors.SSOGoogle(), connectors.SSOOkta(),
	} {
		d := Materialize(raw)
		if d.ConfigSchema[connectors.OIDCPresetKey] == nil {
			t.Fatalf("%s missing preset in schema", d.ID)
		}
	}
}

func TestNotifySentryCallback(t *testing.T) {
	var gotEnabled bool
	var gotDSN, gotEnv string
	var gotRate float64
	s := &Service{OnSentryChange: func(_ context.Context, enabled bool, dsn, environment string, sampleRate float64) {
		gotEnabled, gotDSN, gotEnv, gotRate = enabled, dsn, environment, sampleRate
	}}
	s.notifySentry(context.Background(), connectors.TypeSentry, map[string]any{
		"environment": "dev", "sampleRate": "0.25",
	}, map[string]string{"dsn": "https://a@b/1"}, true)
	if !gotEnabled || gotDSN == "" || gotEnv != "dev" || gotRate != 0.25 {
		t.Fatalf("%v %q %q %v", gotEnabled, gotDSN, gotEnv, gotRate)
	}
	s.notifySentry(context.Background(), connectors.TypeSentry, nil, nil, false)
	if gotEnabled {
		t.Fatal("expected clear")
	}
	called := false
	s.OnSentryChange = func(context.Context, bool, string, string, float64) { called = true }
	s.notifySentry(context.Background(), "slack", map[string]any{}, map[string]string{"dsn": "x"}, true)
	if called {
		t.Fatal("non-sentry must not notify")
	}
}
