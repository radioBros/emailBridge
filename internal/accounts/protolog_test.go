package accounts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestRawDebugTTLCap(t *testing.T) {
	s := &Service{}
	if s.rawDebugTTL() != defaultRawDebugTTL {
		t.Fatal(s.rawDebugTTL())
	}
	s.RawDebugTTL = 48 * time.Hour
	if s.rawDebugTTL() != maxRawDebugTTL {
		t.Fatal(s.rawDebugTTL())
	}
	s.RawDebugTTL = 30 * time.Minute
	if s.rawDebugTTL() != 30*time.Minute {
		t.Fatal(s.rawDebugTTL())
	}
}

func TestViewOwnerNode(t *testing.T) {
	a := Account{ID: "acc_1", Name: "n", Email: "a@b.c"}
	if a.View().OwnerNode != "" {
		t.Fatal("omit when no owner")
	}
	a.Owner = &Owner{NodeID: "node-a", Epoch: 7}
	if a.View().OwnerNode != "node-a" {
		t.Fatalf("ownerNode=%q", a.View().OwnerNode)
	}
	b, _ := json.Marshal(a.View())
	if strings.Contains(string(b), `"epoch"`) || strings.Contains(string(b), "7") {
		t.Fatalf("epoch must not leak: %s", b)
	}
}

func TestViewSwitchesExpiresRawDebug(t *testing.T) {
	past := time.Now().UTC().Add(-time.Minute)
	a := Account{Switches: map[string]any{"protocolLogs.rawDebug": true, "protocolLogs.enabled": true}, RawDebugUntil: &past}
	sw := a.viewSwitches()
	if sw["protocolLogs.rawDebug"] != false || sw["protocolLogs.enabled"] != true {
		t.Fatalf("%+v", sw)
	}
	if a.Switches["protocolLogs.rawDebug"] != true {
		t.Fatal("stored switch must stay until patched")
	}
	future := time.Now().UTC().Add(time.Hour)
	a.RawDebugUntil = &future
	if a.viewSwitches()["protocolLogs.rawDebug"] != true {
		t.Fatal("active raw debug")
	}
}

func TestApplyProtocolSwitchesRoles(t *testing.T) {
	s := &Service{RawDebugTTL: time.Hour}
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := &Account{ID: "acc_x"}

	viewer := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "o", Role: tenancy.RoleViewer,
	})
	if _, err := s.applyProtocolSwitches(viewer, a, map[string]any{"protocolLogs.enabled": true}, now); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer: %v", err)
	}

	operator := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "o", Role: tenancy.RoleOperator,
	})
	if _, err := s.applyProtocolSwitches(operator, a, map[string]any{"protocolLogs.rawDebug": true}, now); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("operator: %v", err)
	}

	admin := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "o", Role: tenancy.RoleAdmin,
	})
	changed, err := s.applyProtocolSwitches(admin, a, map[string]any{"protocolLogs.enabled": true, "protocolLogs.rawDebug": true}, now)
	if err != nil || !changed || a.RawDebugUntil == nil {
		t.Fatalf("admin: changed=%v until=%v err=%v", changed, a.RawDebugUntil, err)
	}
	if d := a.RawDebugUntil.Sub(now); d < time.Hour-time.Second || d > time.Hour+time.Second {
		t.Fatalf("ttl %v", d)
	}

	// Expiry: rawDebugActive flips off; patching enabled alone with raw off clears until.
	expired := now.Add(-time.Minute)
	a.RawDebugUntil = &expired
	a.Switches = map[string]any{"protocolLogs.enabled": true, "protocolLogs.rawDebug": true}
	changed, err = s.applyProtocolSwitches(admin, a, map[string]any{"protocolLogs.enabled": true}, now)
	if err != nil || changed || a.RawDebugUntil != nil {
		t.Fatalf("clear expired: changed=%v until=%v err=%v", changed, a.RawDebugUntil, err)
	}
}

func TestBoolSwitch(t *testing.T) {
	if boolSwitch(nil, "x") || boolSwitch(map[string]any{"x": "yes"}, "x") || !boolSwitch(map[string]any{"x": true}, "x") {
		t.Fatal("boolSwitch")
	}
}
