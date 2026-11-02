package ai

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
)

type fakeAccountSwitches struct {
	val bool
	set bool
}

func (f fakeAccountSwitches) SwitchBool(context.Context, string, string, string) (bool, bool, error) {
	return f.val, f.set, nil
}

func TestAIEnabledAccountOverride(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	svc := &Service{
		Registry: features.DefaultRegistry(), Env: "production",
		Enabled:  nil,
		Accounts: fakeAccountSwitches{val: true, set: true},
	}
	// Without global/org docs, account alone should turn it on (lower wins when set).
	if !svc.aiEnabled(context.Background(), org, "acc_x") {
		t.Fatal("account on")
	}
	svc.Accounts = fakeAccountSwitches{val: false, set: true}
	if svc.aiEnabled(context.Background(), org, "acc_x") {
		t.Fatal("account off")
	}
	svc.Enabled = func(context.Context, string, string) bool { return true }
	if !svc.aiEnabled(context.Background(), org, "acc_x") {
		t.Fatal("override")
	}
}

func TestEnrichMessageReceivedFailOpen(t *testing.T) {
	svc := &Service{Enabled: func(context.Context, string, string) bool { return false }}
	if svc.EnrichMessageReceived(context.Background(), "org", "acc", map[string]any{"subject": "x"}) != nil {
		t.Fatal("disabled")
	}
	svc.Enabled = func(context.Context, string, string) bool { return true }
	svc.Providers = map[string]Provider{ProviderOpenAI: &MockProvider{}}
	svc.Repo = &memStore{}
	// Not configured → fail open (nil).
	if out := svc.EnrichMessageReceived(context.Background(), "org", "acc", map[string]any{
		"text": map[string]any{"plain": "hello world"},
	}); out != nil {
		t.Fatalf("%v", out)
	}
}
