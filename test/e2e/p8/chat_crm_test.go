//go:build e2e

package p8

import (
	"fmt"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Slack chat connector delivers Block Kit–shaped alerts through the installed HTTP endpoint.
func TestP8_SlackChatThroughSink(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	sk := harness.StartSink(t, sink.Options{})
	inst := install(t, owner, "slack", map[string]any{
		"name":   "chat-slack",
		"config": map[string]any{"webhookUrl": sk.URL},
	})
	_ = inst

	user := fmt.Sprintf("slack%d", time.Now().UnixNano()%1_000_000)
	email := user + "@sim.e2e.test"
	accID := connectAccount(t, owner, sim, user, "sim-secret", email, "SlackAcc")
	_ = accID
	time.Sleep(500 * time.Millisecond)
	sk.Reset()

	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "Chat alert", "<chat-p8@e2e>", "hello slack"), nil)
	got, ok := waitSinkBlocks(t, sk, 60*time.Second)
	if !ok {
		t.Fatalf("BUG: %s", "Slack chat route never delivered — MapSlackNewMail uses string(data.from) but data.from is an address object (domain(data.from) is the working helper); suspect internal/integrations/connectors/payloads.go")
	}
	if len(got.Body) == 0 {
		t.Fatal("empty slack body")
	}
}

// HubSpot CRM logs mail via kind=integration webhook pipeline against an httptest fake.
func TestP8_HubSpotAction(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	hs, engages := fakeHubSpot(t)
	inst := install(t, owner, "hubspot", map[string]any{
		"name": "hs-e2e",
		"config": map[string]any{
			"baseUrl": hs.URL,
			"filter":  `type == "message.received"`,
		},
		"secrets": map[string]string{"accessToken": "tok-hs"},
	})
	id := inst.Str("id")

	user := fmt.Sprintf("hs%d", time.Now().UnixNano()%1_000_000)
	email := user + "@sim.e2e.test"
	_ = connectAccount(t, owner, sim, user, "sim-secret", email, "HSAcc")
	time.Sleep(500 * time.Millisecond)

	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "CRM subject", "<hs-p8@e2e>", "body"), nil)
	waitHits(t, engages, 1, 90*time.Second, "hubspot engagement")
	view := waitLastActionOK(t, owner, id, 60*time.Second)
	// detail may be the remote id "e1"
	if d, _ := view.JSON["lastActionDetail"].(string); d != "" && d != "e1" {
		t.Logf("lastActionDetail=%q", d)
	}
}

// Pipedrive CRM logs mail the same way.
func TestP8_PipedriveAction(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	pd, notes := fakePipedrive(t)
	inst := install(t, owner, "pipedrive", map[string]any{
		"name": "pd-e2e",
		"config": map[string]any{
			"baseUrl":       pd.URL,
			"companyDomain": "acme",
			"filter":        `type == "message.received"`,
		},
		"secrets": map[string]string{"apiToken": "tok-pd"},
	})
	id := inst.Str("id")

	user := fmt.Sprintf("pd%d", time.Now().UnixNano()%1_000_000)
	email := user + "@sim.e2e.test"
	_ = connectAccount(t, owner, sim, user, "sim-secret", email, "PDAcc")
	time.Sleep(500 * time.Millisecond)

	sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, "PD subject", "<pd-p8@e2e>", "body"), nil)
	waitHits(t, notes, 1, 90*time.Second, "pipedrive note")
	_ = waitLastActionOK(t, owner, id, 60*time.Second)
}
