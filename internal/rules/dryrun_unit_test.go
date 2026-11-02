package rules

import (
	"testing"
	"time"
)

func TestEventDocToMap(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	d := dryRunEventDoc{
		ID: "evt_1", AccountID: "acc_1", Type: "message.received",
		Time: now, Subject: "subj", Data: `{"subject":"hi"}`, Schema: 1,
	}
	m := eventDocToMap(d, "org_1")
	if m["id"] != "evt_1" || m["type"] != "message.received" || m["eborg"] != "org_1" {
		t.Fatalf("%v", m)
	}
	if m["source"] != "/organizations/org_1/accounts/acc_1" {
		t.Fatal(m["source"])
	}
	data, ok := m["data"].(map[string]any)
	if !ok || data["subject"] != "hi" {
		t.Fatalf("%v", m["data"])
	}
	bad := eventDocToMap(dryRunEventDoc{ID: "e2", Type: "t", Data: "{", Time: now}, "org_x")
	if bad["source"] != "/organizations/org_x" {
		t.Fatal(bad["source"])
	}
	empty, _ := bad["data"].(map[string]any)
	if len(empty) != 0 {
		t.Fatal(empty)
	}
}

func TestRegisterHandlers_NilJobs(t *testing.T) {
	r := &Repo{}
	r.RegisterHandlers() // no panic
}
