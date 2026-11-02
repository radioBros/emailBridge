//go:build e2e

package p2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	gosync "sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// P2.8 (S14 §4 P2, PLAN "zero-downtime rolling upgrades"): the sync nodes are upgraded one at a time — drain
// through the API (accounts are handed to the other node, which connects before the old owner lets go), stop,
// start again — while mail keeps arriving. Every message is delivered exactly once, no account ever has more
// than the hand-off's two sessions open at once, and logins stay at one per move (no reconnect storm).
func TestP2_RollingUpgradeWithHandOff(t *testing.T) {
	const accounts = 6
	sim := harness.StartSim(t)
	api := harness.StartP1Node(t, harness.ClusterEnv(t, "ru-api", "api,scheduler,webhook"))
	syncNodes := []*harness.Node{
		harness.StartPeer(t, api, "ru-sync-1", "sync,submit", nil),
		harness.StartPeer(t, api, "ru-sync-2", "sync,submit", nil),
	}
	owner, _ := api.OwnerSession(t)
	t.Cleanup(func() {
		if t.Failed() {
			clusterState(t, api)
		}
	})

	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "ru-ep", "kind": "http", "url": sk.URL, "secret": harness.WebhookSecret(),
		"events": []string{"message.received"},
		"retry":  map[string]any{"maxAttempts": 10, "backoffBase": "500ms", "backoffJitter": 0.0, "timeout": "3s"},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)

	stamp := time.Now().UnixNano()
	users := make([]string, accounts)
	emails := make([]string, accounts)
	ids := make([]string, accounts)
	for i := range accounts {
		users[i] = fmt.Sprintf("ru%d-%d", stamp, i)
		emails[i] = users[i] + "@sim.e2e.test"
		sim.CreateSimUser(t, users[i], "sim-secret")
		body := harness.AccountCreateBody(t, sim, users[i], "sim-secret", emails[i],
			fmt.Sprintf("RU %d", i), map[string]any{"sandbox.enabled": false}, "standard")
		body["skipVerify"] = true // the live verify is limited to 5 per minute per organisation
		acc := owner.Do(http.MethodPost, "/v1/accounts", body)
		harness.ExpectStatus(t, acc, http.StatusCreated)
		ids[i] = acc.Str("id")
	}
	allConnected := func(what string) map[string]int {
		t.Helper()
		var placed map[string]int
		harness.WaitUntil(t, 120*time.Second, what, func() bool {
			placed = map[string]int{}
			for _, id := range ids {
				r := owner.Do(http.MethodGet, "/v1/accounts/"+id, nil)
				if r.Status != http.StatusOK || r.Str("state") != "connected" || r.Str("ownerNode") == "" {
					return false
				}
				placed[r.Str("ownerNode")]++
			}
			return true
		})
		return placed
	}
	t.Logf("placement: %v", allConnected("all accounts connected"))
	time.Sleep(3 * time.Second) // silent first syncs settle
	sk.Reset()
	before := make([]int64, accounts)
	for i, u := range users {
		before[i] = sessions(t, sim, u).Logins
	}

	// Mail keeps arriving through the whole upgrade.
	ctx, stopInject := context.WithCancel(context.Background())
	var wg gosync.WaitGroup
	sent := map[string]bool{}
	var mu gosync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(250 * time.Millisecond)
		defer tick.Stop()
		for n := 0; ; n++ {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			i := n % accounts
			mid := fmt.Sprintf("<ru-%d@e2e>", n)
			raw := harness.RawMessage(emails[i], emails[i], fmt.Sprintf("ru %d", n), mid, "x")
			if _, err := sim.Client.InjectMessage(ctx, users[i], "INBOX", raw, nil, time.Now()); err != nil {
				if ctx.Err() == nil {
					t.Errorf("inject %s: %v", mid, err) // never Fatal off the test goroutine
				}
				continue
			}
			mu.Lock()
			sent[mid] = true
			mu.Unlock()
		}
	}()

	moves := 0
	for _, node := range syncNodes {
		id := node.Env("EB_NODE_ID")
		harness.ExpectStatus(t, owner.Do(http.MethodPost, "/v1/cluster/nodes/"+id+"/drain", nil), http.StatusAccepted)
		harness.WaitUntil(t, 90*time.Second, id+" drained", func() bool {
			n, ok := clusterNode(t, owner, id)
			return ok && n.Draining && n.Accounts == 0
		})
		placed := allConnected("accounts handed off from " + id)
		if placed[id] != 0 {
			t.Fatalf("%s still owns accounts after the drain: %v", id, placed)
		}
		moves += accounts // upper bound: every account may have moved
		node.Stop()       // drained: nothing left to hand off, a plain stop is the upgrade's restart
		time.Sleep(time.Second)
		node.Restart()
		harness.WaitUntil(t, 60*time.Second, id+" back and not draining", func() bool {
			n, ok := clusterNode(t, owner, id)
			return ok && !n.Draining
		})
		t.Logf("after upgrading %s: %v", id, allConnected("all accounts connected after "+id))
		moves += accounts // rebalancing back onto the new process
	}
	time.Sleep(2 * time.Second)
	stopInject()
	wg.Wait()

	// Exactly once: every injected Message-ID arrives, no event twice, no message under two events.
	mu.Lock()
	want := len(sent)
	mu.Unlock()
	harness.WaitUntil(t, 120*time.Second, fmt.Sprintf("%d deliveries", want), func() bool {
		return len(harness.FilterEvents(sk.Requests(), "message.received")) >= want
	})
	time.Sleep(3 * time.Second) // room for a wrong extra delivery
	byEvent, byMessage := map[string]int{}, map[string]int{}
	for _, r := range harness.FilterEvents(sk.Requests(), "message.received") {
		byEvent[r.Headers.Get("webhook-id")]++
		var env struct {
			Data struct {
				MessageIDHeader string `json:"messageIdHeader"`
			} `json:"data"`
		}
		_ = json.Unmarshal(r.Body, &env)
		byMessage[env.Data.MessageIDHeader]++
	}
	for id, c := range byEvent {
		if c != 1 {
			t.Errorf("event %s delivered %d times", id, c)
		}
	}
	mu.Lock()
	for mid := range sent {
		if byMessage[mid] != 1 {
			t.Errorf("message %s: %d events, want 1", mid, byMessage[mid])
		}
	}
	mu.Unlock()
	t.Logf("injected %d, delivered %d events", want, len(byEvent))

	// After the upgrade the accounts spread over both new processes again (rebalancing onto a returning node).
	harness.WaitUntil(t, 120*time.Second, "accounts rebalanced over both sync nodes", func() bool {
		placed := allConnected("all connected")
		return placed["ru-sync-1"] > 0 && placed["ru-sync-2"] > 0
	})

	// No reconnect storm: at most the hand-off's two sessions at once, and no more logins than moves.
	var logins int64
	for i, u := range users {
		st := sessions(t, sim, u)
		if st.PeakOpen > 2 {
			t.Errorf("%s: %d sessions open at once", u, st.PeakOpen)
		}
		logins += st.Logins - before[i]
	}
	t.Logf("logins during the upgrade: %d (bound %d)", logins, moves)
	if logins > int64(moves) {
		t.Errorf("%d logins for at most %d account moves: reconnect storm", logins, moves)
	}
}

type nodeView struct {
	ID       string `json:"id"`
	Draining bool   `json:"draining"`
	Accounts int    `json:"accounts"`
}

func clusterNode(t *testing.T, c *harness.Client, id string) (nodeView, bool) {
	t.Helper()
	r := c.Do(http.MethodGet, "/v1/cluster", nil)
	if r.Status != http.StatusOK {
		t.Fatalf("GET /v1/cluster = %d %s", r.Status, r.Body)
	}
	var v struct {
		Nodes []nodeView `json:"nodes"`
	}
	if err := json.Unmarshal(r.Body, &v); err != nil {
		t.Fatal(err)
	}
	for _, n := range v.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return nodeView{}, false
}

func sessions(t *testing.T, sim *harness.SimEnv, user string) struct{ Logins, PeakOpen int64 } {
	t.Helper()
	st := sim.Sim.SessionStats(user)
	return struct{ Logins, PeakOpen int64 }{st.Logins, st.PeakOpen}
}
