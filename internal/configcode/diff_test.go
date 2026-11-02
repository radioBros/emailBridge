package configcode

import (
	"strings"
	"testing"
)

func TestBuildPlanCreateUpdateDeletePrune(t *testing.T) {
	desired := &Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Metadata: Metadata{Organization: "acme"},
		Spec: Spec{
			Settings: map[string]any{"webhookTimeout": "20s"},
			Switches: map[string]any{"tracking.opens": false},
			WebhookEndpoints: []EndpointSpec{
				{Name: "billing", URL: "https://b.example/h", Events: []string{"message.received"},
					Secret: "${secret:BILLING_WEBHOOK_SECRET}"},
				{Name: "crm", URL: "https://crm.example/h", Events: []string{"*"}},
			},
			Routes: []RouteSpec{
				{Name: "to-billing", Endpoint: "billing", Filter: "true"},
			},
			APIKeys: []APIKeySpec{
				{Name: "ci", Scopes: []string{"events:read"}},
			},
		},
	}
	en := true
	live := &liveSnapshot{
		Settings:  map[string]any{"webhookTimeout": "10s"},
		Switches:  map[string]any{"tracking.opens": true, "old.switch": true},
		SettingsV: 2,
		Endpoints: map[string]liveEndpoint{
			"billing": {ID: "ep_1", Version: 3, HasSecret: true, Spec: EndpointSpec{
				Name: "billing", URL: "https://old.example/h", Events: []string{"message.received"}, Enabled: &en,
			}},
			"orphan": {ID: "ep_x", Version: 1, Spec: EndpointSpec{Name: "orphan", URL: "https://x", Enabled: &en}},
		},
		Routes: map[string]liveRoute{
			"old-route": {ID: "rt_1", Version: 1, EndpointName: "orphan", Spec: RouteSpec{Name: "old-route", Endpoint: "orphan"}},
		},
		APIKeys: map[string]liveAPIKey{
			"legacy": {ID: "key_1", Version: 1, Spec: APIKeySpec{Name: "legacy", Scopes: []string{"read"}}},
		},
	}

	plan, err := BuildPlan(desired, live, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.PlanHash == "" || !strings.HasPrefix(plan.PlanHash, "sha256:") {
		t.Fatalf("hash=%q", plan.PlanHash)
	}

	ops := map[string]string{}
	var order []string
	for _, a := range plan.Actions {
		key := a.Kind + ":" + a.Name
		ops[key] = a.Op
		order = append(order, a.Op+":"+a.Kind+":"+a.Name)
		for _, v := range a.Changes {
			s, _ := v.(string)
			if strings.Contains(s, "whsec_") {
				t.Fatalf("secret leaked in changes: %#v", a.Changes)
			}
			if s != "" && s != "(changed)" && s != "(unchanged)" && strings.Contains(strings.ToLower(s), "password") {
				t.Fatalf("secret leaked in changes: %#v", a.Changes)
			}
		}
		if sec, ok := a.Changes["secret"]; ok {
			if sec != "(changed)" && sec != "(unchanged)" {
				t.Fatalf("secret not redacted: %#v", sec)
			}
		}
	}

	if ops["settings:organization"] != OpUpdate {
		t.Fatalf("settings: %v", ops)
	}
	if ops["switch:tracking.opens"] != OpUpdate {
		t.Fatalf("switch update: %v", ops)
	}
	if ops["switch:old.switch"] != OpDelete {
		t.Fatalf("switch prune: %v", ops)
	}
	if ops["webhookEndpoint:billing"] != OpUpdate {
		t.Fatalf("endpoint update: %v", ops)
	}
	if ops["webhookEndpoint:crm"] != OpCreate {
		t.Fatalf("endpoint create: %v", ops)
	}
	if ops["webhookEndpoint:orphan"] != OpDelete {
		t.Fatalf("endpoint prune: %v", ops)
	}
	if ops["route:to-billing"] != OpCreate {
		t.Fatalf("route create: %v", ops)
	}
	if ops["route:old-route"] != OpDelete {
		t.Fatalf("route prune: %v", ops)
	}
	if ops["apiKey:ci"] != OpCreate || ops["apiKey:legacy"] != OpDelete {
		t.Fatalf("keys: %v", ops)
	}

	// dependency order: endpoint creates before route creates; route deletes before endpoint deletes
	crmIdx, routeIdx := indexOf(order, "create:webhookEndpoint:crm"), indexOf(order, "create:route:to-billing")
	if crmIdx < 0 || routeIdx < 0 || crmIdx > routeIdx {
		t.Fatalf("endpoint create must precede route create: %v", order)
	}
	delRoute, delEP := indexOf(order, "delete:route:old-route"), indexOf(order, "delete:webhookEndpoint:orphan")
	if delRoute < 0 || delEP < 0 || delRoute > delEP {
		t.Fatalf("route delete must precede endpoint delete: %v", order)
	}
}

func TestBuildPlanIdempotent(t *testing.T) {
	en := true
	desired := &Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Spec: Spec{
			WebhookEndpoints: []EndpointSpec{
				{Name: "billing", URL: "https://b.example/h", Events: []string{"message.received"}, Enabled: &en},
			},
		},
	}
	live := &liveSnapshot{
		Endpoints: map[string]liveEndpoint{
			"billing": {ID: "ep_1", Version: 1, Spec: EndpointSpec{
				Name: "billing", URL: "https://b.example/h", Events: []string{"message.received"}, Enabled: &en, Kind: "http",
			}},
		},
		Settings: map[string]any{}, Switches: map[string]any{},
		Routes: map[string]liveRoute{}, APIKeys: map[string]liveAPIKey{},
	}
	p1, err := BuildPlan(desired, live, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Actions) != 0 {
		t.Fatalf("expected empty plan, got %+v", p1.Actions)
	}
	p2, err := BuildPlan(desired, live, false)
	if err != nil {
		t.Fatal(err)
	}
	if p1.PlanHash != p2.PlanHash {
		t.Fatalf("hash drift on idempotent plan")
	}
}

func TestBuildPlanRejectsInlineSecret(t *testing.T) {
	_, err := BuildPlan(&Document{
		Spec: Spec{WebhookEndpoints: []EndpointSpec{{Name: "x", URL: "https://x", Secret: "whsec_abc"}}},
	}, nil, false)
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestSecretRefs(t *testing.T) {
	if !IsSecretRef("${secret:FOO}") || !IsSecretRef("${FOO}") {
		t.Fatal("refs")
	}
	n, ok := SecretName("${secret:FOO}")
	if !ok || n != "FOO" {
		t.Fatal(n, ok)
	}
	n, ok = SecretName("${FOO}")
	if !ok || n != "FOO" {
		t.Fatal(n, ok)
	}
	if _, ok := SecretName("inline"); ok {
		t.Fatal("inline is not a ref")
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}
