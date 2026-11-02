//go:build e2e

// Package p2 holds the P2 end-to-end scenarios: several processes with split roles (S15 §3, S4 §6).
package p2

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

// Roles split over separate processes: the api node only serves the API and runs the scheduler, the sync node
// owns the mailbox, the webhook node delivers. Killing the webhook node and then the sync node moves their
// work to replacements; every event is delivered exactly once (webhook-id is the event ID, S7).
func TestP2_ClusterRolesAndFailover(t *testing.T) {
	sim := harness.StartSim(t)
	api := harness.StartP1Node(t, harness.ClusterEnv(t, "e2e-api", "api,scheduler"))
	syncNode := harness.StartPeer(t, api, "e2e-sync-1", "sync,submit", nil)
	wh := harness.StartPeer(t, api, "e2e-webhook-1", "webhook", nil)
	owner, _ := api.OwnerSession(t)
	t.Cleanup(func() {
		if t.Failed() {
			clusterState(t, api)
		}
	})

	user, pass := fmt.Sprintf("p2%d", time.Now().UnixNano()), "sim-secret"
	email := user + "@sim.e2e.test"
	sim.CreateSimUser(t, user, pass)
	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "p2-ep", "kind": "http", "url": sk.URL, "secret": harness.WebhookSecret(),
		"events": []string{"message.received"},
		"retry":  map[string]any{"maxAttempts": 10, "backoffBase": "500ms", "backoffJitter": 0.0, "timeout": "3s"},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	acc := owner.Do(http.MethodPost, "/v1/accounts", harness.AccountCreateBody(t, sim, user, pass, email, "P2", map[string]any{"sandbox.enabled": false}, "standard"))
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+accID, 60*time.Second, "connected on the sync node", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected" && r.Str("ownerNode") == "e2e-sync-1"
	})

	deliveredOnce := func(n int) {
		t.Helper()
		harness.WaitUntil(t, 90*time.Second, fmt.Sprintf("%d message.received deliveries", n), func() bool {
			return len(harness.FilterEvents(sk.Requests(), "message.received")) >= n
		})
		time.Sleep(2 * time.Second) // room for a wrong second delivery
		seen := map[string]int{}
		for _, r := range harness.FilterEvents(sk.Requests(), "message.received") {
			seen[r.Headers.Get("webhook-id")]++
		}
		if len(seen) != n {
			t.Fatalf("distinct events delivered: %d, want %d (%v)", len(seen), n, seen)
		}
		for id, c := range seen {
			if c != 1 {
				t.Fatalf("event %s delivered %d times", id, c)
			}
		}
	}
	inject := func(i int) {
		t.Helper()
		sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, fmt.Sprintf("p2 %d", i), fmt.Sprintf("<p2-%d@e2e>", i), "x"), nil)
	}

	inject(1)
	deliveredOnce(1)

	// The webhook node dies: deliveries wait, and resume on a replacement once its partition leases expire.
	wh.Kill()
	inject(2)
	time.Sleep(time.Second)
	_ = harness.StartPeer(t, api, "e2e-webhook-2", "webhook", nil)
	deliveredOnce(2)

	// The sync node dies: the account moves to a replacement after its lease expires; mail is not lost.
	syncNode.Kill()
	_ = harness.StartPeer(t, api, "e2e-sync-2", "sync,submit", nil)
	owner.WaitJSON(t, "/v1/accounts/"+accID, 90*time.Second, "account moved to e2e-sync-2", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("ownerNode") == "e2e-sync-2" && r.Str("state") == "connected"
	})
	inject(3)
	deliveredOnce(3)
}

// clusterState logs the durable state that tells where a missing delivery stopped: events in the outbox and
// whether the relay published them, and the deliveries the router created.
func clusterState(t *testing.T, n *harness.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cl, err := mongo.Connect(options.Client().ApplyURI(n.MongoURI))
	if err != nil {
		t.Logf("state: %v", err)
		return
	}
	defer func() { _ = cl.Disconnect(ctx) }()
	db := cl.Database(n.Database)
	for _, q := range []struct {
		coll   string
		filter bson.M
	}{
		{"events", bson.M{"type": "message.received"}},
		{"events", bson.M{"type": "message.received", "published": true}},
		{"events", bson.M{"published": false}},
		{"deliveries", bson.M{}},
		{"deliveries", bson.M{"state": "succeeded"}},
		{"accounts", bson.M{"state": "connected"}},
	} {
		c, err := db.Collection(q.coll).CountDocuments(ctx, q.filter)
		t.Logf("state: %s %v → %d (%v)", q.coll, q.filter, c, err)
	}
	// Lease holders and the node registry, through the bus (only internal/bus may talk to NATS).
	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeExternal, NodeID: "e2e-diag", URL: "nats://" + n.Env("EB_NATS_EMBEDDED_LISTEN")})
	if err != nil {
		t.Logf("state: bus %v", err)
		return
	}
	defer b.Close()
	nodes, _ := b.LiveNodes(ctx)
	for _, node := range nodes {
		t.Logf("state: node %s roles=%v draining=%v seen=%s", node.NodeID, node.Roles, node.Draining, node.SeenAt.Format(time.TimeOnly))
	}
	free := []string{}
	for p := range 64 {
		if h, err := b.LeaseHolder(ctx, fmt.Sprintf("relay.%d", p)); err == nil && h == nil {
			free = append(free, fmt.Sprint(p))
		}
	}
	t.Logf("state: unheld relay partitions: %s", strings.Join(free, ","))
}
