//go:build e2e

// Package p9 is the P9 end-to-end suite: PEC against the provider simulator's PEC mode (S17 §12).
package p9

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/simulator"
	simpec "github.com/radiobros/emailbridge/simulator/pec"
	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// A PEC mailbox sends with atMostOnce to one certified recipient that exists and one that does not: the
// provider's acceptance, delivery and error receipts become pec.receipt webhooks with valid, trusted
// signatures, the outbound message ends partiallyDelivered, and the receipts API serves the signed raw source.
func TestP9_PECSendAndReceipts(t *testing.T) {
	ctx := context.Background()
	sim := harness.StartSimOpts(t, simulator.Options{PEC: &simpec.Config{
		Domains: []simpec.Domain{{Name: "pec.sim.test", Provider: "Gestore Simulato S.p.A.", // Italian provider name
			SystemAddress: "posta-certificata@pec.sim.test"}},
		SlowDelay: 50 * time.Millisecond,
	}})
	caPEM, err := sim.Client.PECCA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	caPath := filepath.Join(t.TempDir(), "pec-ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	n := harness.StartP1Node(t, map[string]string{"EB_PEC_TRUST_ANCHORS": caPath})
	owner, _ := n.OwnerSession(t)

	stamp := time.Now().UnixNano()
	sender := fmt.Sprintf("sender%d@pec.sim.test", stamp)
	rcpt := fmt.Sprintf("rcpt%d@pec.sim.test", stamp)
	pass := "sim-secret"
	sim.CreateSimUser(t, sender, pass)
	sim.CreateSimUser(t, rcpt, pass)

	hooks := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "pec-e2e", "kind": "http", "url": hooks.URL, "events": []string{"pec.receipt"},
		"secret": harness.WebhookSecret(),
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	body := harness.AccountCreateBody(t, sim, sender, pass, sender, "PEC sender", map[string]any{
		"pec.enabled": true, "sandbox.enabled": false,
	}, "atMostOnce")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})

	sent := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/outbound-messages", map[string]any{
		"to":        []map[string]any{{"address": rcpt}, {"address": "nouser@pec.sim.test"}},
		"subject":   "Verbale 42",
		"text":      "Certified hello.",
		"messageId": fmt.Sprintf("<verbale-%d@legacyapp.test>", stamp),
	})
	harness.ExpectStatus(t, sent, http.StatusAccepted)
	outID := sent.Str("id")
	if outID == "" {
		t.Fatalf("submit: %s", sent.Body)
	}

	owner.WaitJSON(t, "/v1/outbound-messages/"+outID, 90*time.Second, "pec state partiallyDelivered", func(r harness.Resp) bool {
		pec, _ := r.JSON["pec"].(map[string]any)
		return r.Status == http.StatusOK && pec != nil && pec["state"] == "partiallyDelivered"
	})

	// Webhooks: acceptance, delivery, delivery error — each signed and trusted.
	if !hooks.WaitFor(3, 30*time.Second) {
		t.Fatalf("pec.receipt webhooks: %d", len(hooks.Requests()))
	}
	types := map[string]bool{}
	for _, req := range hooks.Requests() {
		var env struct {
			Type string `json:"type"`
			Data struct {
				Type       string `json:"type"`
				OutboundID string `json:"outboundId"`
				Signature  struct {
					Valid   bool  `json:"valid"`
					Trusted *bool `json:"trusted"`
				} `json:"signature"`
			} `json:"data"`
		}
		if err := json.Unmarshal(req.Body, &env); err != nil || env.Type != "pec.receipt" {
			continue
		}
		if env.Data.OutboundID != outID || !env.Data.Signature.Valid || env.Data.Signature.Trusted == nil || !*env.Data.Signature.Trusted {
			t.Fatalf("pec.receipt %s: %s", env.Data.Type, req.Body)
		}
		types[env.Data.Type] = true
	}
	for _, want := range []string{"accettazione", "avvenuta-consegna", "errore-consegna"} {
		if !types[want] {
			t.Fatalf("missing %s receipt (got %v)", want, types)
		}
	}

	// Receipts API + signed raw source with its digest.
	list := owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/pec/receipts?includeTotal=true", nil)
	harness.ExpectStatus(t, list, http.StatusOK)
	data, _ := list.JSON["data"].([]any)
	if len(data) < 3 {
		t.Fatalf("receipts list: %s", list.Body)
	}
	first, _ := data[0].(map[string]any)
	rcpID, _ := first["id"].(string)
	raw := owner.Do(http.MethodGet, "/v1/pec/receipts/"+rcpID+"/raw", nil)
	harness.ExpectStatus(t, raw, http.StatusOK)
	if !strings.Contains(string(raw.Body), "daticert.xml") || !strings.HasPrefix(raw.Header.Get("Digest"), "sha-256=") {
		t.Fatalf("raw receipt: digest %q, %d bytes", raw.Header.Get("Digest"), len(raw.Body))
	}
}
