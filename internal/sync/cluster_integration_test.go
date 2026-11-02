//go:build integration

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

// clusterAccounts is a multi-account source writing the S1 fields the engine relies on, recording claims and
// capacity marks.
type clusterAccounts struct {
	db       *store.DB
	mu       gosync.Mutex
	order    []string
	accs     map[string]Account
	claims   map[string][]string
	unplaced map[string]int
}

func (a *clusterAccounts) Placeable(context.Context) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.order...), nil
}

func (a *clusterAccounts) Load(_ context.Context, id string) (*Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.accs[id]
	return &c, nil
}

func (a *clusterAccounts) Claimed(ctx context.Context, id, node string, epoch uint64) ([]Command, error) {
	a.mu.Lock()
	a.claims[id] = append(a.claims[id], node)
	a.mu.Unlock()
	_, err := a.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"owner": bson.M{"nodeId": node, "epoch": int64(epoch), "since": time.Now()}}})
	return nil, err
}

func (a *clusterAccounts) Transition(ctx context.Context, id string, epoch uint64, to string, _ *types.Reason) error {
	res, err := a.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": id, "owner.epoch": int64(epoch)}, bson.M{"$set": bson.M{"state": to}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return index.ErrFenced
	}
	return nil
}

func (a *clusterAccounts) Unplaced(_ context.Context, id string) error {
	a.mu.Lock()
	a.unplaced[id]++
	a.mu.Unlock()
	return nil
}

func (a *clusterAccounts) claimsOf(id string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.claims[id]...)
}

// msgSink records log messages.
type msgSink struct {
	mu   gosync.Mutex
	msgs []string
}

func (s *msgSink) Enabled(context.Context, slog.Level) bool { return true }
func (s *msgSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, r.Message)
	return nil
}
func (s *msgSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *msgSink) WithGroup(string) slog.Handler      { return s }
func (s *msgSink) count(msg string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, m := range s.msgs {
		if m == msg {
			n++
		}
	}
	return n
}

type cluster struct {
	db         *store.DB
	busA, busB *bus.Bus
	accs       *clusterAccounts
	sim        *simtest.Env
	users      map[string]string // accountID → simulator user
	ids        []string
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// clusterSetup builds n simulator accounts and two bus connections with their own node IDs: n1 runs the
// embedded NATS server, n2 connects to it like a second node. At least two accounts are placed on n2.
func clusterSetup(t *testing.T, n int) *cluster {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_cluster_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, m := range []store.Migration{index.Migration(1), events.Migration(2)} {
		if err := m.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	listen := freeAddr(t)
	busA, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir(), Listen: listen})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(busA.Close)
	busB, err := bus.Open(ctx, bus.Options{Mode: bus.ModeExternal, NodeID: "n2", URL: "nats://" + listen})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(busB.Close)

	sim := simtest.Start(t)
	c := &cluster{db: db, busA: busA, busB: busB, sim: sim, users: map[string]string{},
		accs: &clusterAccounts{db: db, accs: map[string]Account{}, claims: map[string][]string{}, unplaced: map[string]int{}}}
	org := store.NewID(store.PrefixOrganization)
	onN2 := 0
	for len(c.ids) < n || onN2 < 2 {
		id := store.NewID(store.PrefixAccount)
		if bus.OwnerOf(LeasePrefix+id, []string{"n1", "n2"}) == "n2" {
			if onN2 >= n-2 && len(c.ids)-onN2 < 2 { // keep at least two on n1 as well
				continue
			}
			onN2++
		} else if len(c.ids)-onN2 >= n-2 && onN2 < 2 {
			continue
		}
		user := fmt.Sprintf("c%d", time.Now().UnixNano())
		if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Collection(index.Accounts).InsertOne(ctx, bson.M{"_id": id, "organizationId": org, "state": StateNew}); err != nil {
			t.Fatal(err)
		}
		c.accs.accs[id] = Account{ID: id, OrganizationID: org, Email: user + "@sim.test", State: StateNew,
			IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone, IMAPUser: user, IMAPPass: "pw",
			Paths: []string{"INBOX"}, IdlePaths: []string{"INBOX"}, NotifyFrom: time.Now().Add(-time.Hour)}
		c.accs.order = append(c.accs.order, id)
		c.users[id] = user
		c.ids = append(c.ids, id)
	}
	return c
}

func (c *cluster) manager(b *bus.Bus, node string, log *slog.Logger) *Manager {
	return &Manager{NodeID: node, Bus: b, Accounts: c.accs, Index: &index.Index{DB: c.db}, Events: &events.Writer{DB: c.db},
		Config:   Config{ResyncDelay: time.Hour, ReconnectMin: 200 * time.Millisecond, ReconnectMax: time.Second},
		LeaseTTL: 3 * time.Second, Tick: 200 * time.Millisecond, HandoffTimeout: 15 * time.Second, Log: log}
}

func (c *cluster) count(typ string) int64 {
	n, _ := c.db.Collection(events.Collection).CountDocuments(context.Background(), bson.M{"type": typ})
	return n
}

func run(m *Manager) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	var once gosync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}

func (c *cluster) injectAll(t *testing.T, tag string) {
	t.Helper()
	for _, id := range c.ids {
		user := c.users[id]
		if _, err := c.sim.Client.InjectMessage(context.Background(), user, "INBOX", raw(tag+" "+user, "<"+tag+"-"+user+"@x>"), nil, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
}

// Placement spreads accounts over the sync nodes; when a node joins, accounts move to it through pre-connected
// hand-offs (not timeouts), nothing is lost or reported twice, and a draining node gives its accounts back.
func TestIntegrationClusterHandoffAndDrain(t *testing.T) {
	c := clusterSetup(t, 6)
	ctx := context.Background()
	if err := c.busA.RegisterNode(ctx, bus.NodeInfo{NodeID: "n1", Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	logA := &msgSink{}
	a := c.manager(c.busA, "n1", slog.New(logA))
	stopA := run(a)
	defer stopA()
	waitFor(t, 40*time.Second, "n1 did not take every account", func() bool {
		return len(a.Owned()) == len(c.ids) && c.count(events.AccountInitialized) == int64(len(c.ids))
	})

	if err := c.busB.RegisterNode(ctx, bus.NodeInfo{NodeID: "n2", Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	b := c.manager(c.busB, "n2", slog.New(slog.DiscardHandler))
	stopB := run(b)
	defer stopB()
	var onB []string
	for _, id := range c.ids {
		if bus.OwnerOf(LeasePrefix+id, []string{"n1", "n2"}) == "n2" {
			onB = append(onB, id)
		}
	}
	waitFor(t, 40*time.Second, "accounts did not move to n2", func() bool {
		owned := b.Owned()
		return slices.Equal(owned, sortedCopy(onB)) && len(a.Owned())+len(owned) == len(c.ids)
	})
	if got := logA.count("account handed off"); got != len(onB) {
		t.Fatalf("hand-offs: %d, want %d (timeouts: %d)", got, len(onB), logA.count("hand-off target did not take the account in time; releasing it"))
	}
	for _, id := range onB {
		if cl := c.accs.claimsOf(id); len(cl) != 2 || cl[0] != "n1" || cl[1] != "n2" {
			t.Fatalf("claims of %s: %v", id, cl)
		}
	}
	if got := c.count(events.AccountInitialized); got != int64(len(c.ids)) {
		t.Fatalf("a hand-off re-initialised an account: %d initialised", got)
	}

	c.injectAll(t, "after-handoff")
	c.waitReceived(t, int64(len(c.ids)), a, b, "mail after the hand-off not reported exactly once", "after-handoff")

	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	b.Drain(dctx)
	cancel()
	if len(b.Owned()) != 0 {
		t.Fatalf("n2 still holds %v after draining", b.Owned())
	}
	waitFor(t, 30*time.Second, "n1 did not take the drained accounts back", func() bool { return len(a.Owned()) == len(c.ids) })
	nodes, _ := c.busA.LiveNodes(ctx)
	drained := false
	for _, n := range nodes {
		drained = drained || (n.NodeID == "n2" && n.Draining)
	}
	if !drained {
		t.Fatalf("n2 not marked draining in eb-nodes: %+v", nodes)
	}
	c.injectAll(t, "after-drain")
	c.waitReceived(t, int64(2*len(c.ids)), a, b, "mail after the drain not reported exactly once", "after-drain")
	time.Sleep(time.Second)
	if got := c.count(events.MessageReceived); got != int64(2*len(c.ids)) {
		t.Fatalf("duplicates: %d message.received, want %d", got, 2*len(c.ids))
	}
}

// A node at EB_SYNC_MAX_ACCOUNTS takes no more accounts and records the capacity reason for the others.
func TestIntegrationClusterCapacity(t *testing.T) {
	c := clusterSetup(t, 4)
	ctx := context.Background()
	if err := c.busA.RegisterNode(ctx, bus.NodeInfo{NodeID: "n1", Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	logA := &msgSink{}
	a := c.manager(c.busA, "n1", slog.New(logA))
	a.MaxAccounts = 2
	stop := run(a)
	defer stop()
	waitFor(t, 30*time.Second, "capacity not applied", func() bool {
		return len(a.Owned()) == 2 && a.Stats().Unplaced == int64(len(c.ids)-2)
	})
	c.accs.mu.Lock()
	marked := len(c.accs.unplaced)
	c.accs.mu.Unlock()
	if marked != len(c.ids)-2 || logA.count("no sync capacity for account") == 0 {
		t.Fatalf("unplaced marks: %d, want %d", marked, len(c.ids)-2)
	}
	time.Sleep(time.Second)
	c.accs.mu.Lock()
	defer c.accs.mu.Unlock()
	for id, n := range c.accs.unplaced {
		if n != 1 {
			t.Fatalf("%s marked %d times within one lease TTL", id, n)
		}
	}
}

// waitReceived waits for exactly want message.received events; on timeout it reports, per account, the count
// and the owner, so a loss is told apart from a duplicate.
func (c *cluster) waitReceived(t *testing.T, want int64, a, b *Manager, what, tag string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if c.count(events.MessageReceived) == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	var lines []string
	for _, id := range c.ids {
		n, _ := c.db.Collection(events.Collection).CountDocuments(context.Background(), bson.M{"type": events.MessageReceived, "accountId": id})
		owner := "none"
		switch {
		case slices.Contains(a.Owned(), id):
			owner = "n1"
		case slices.Contains(b.Owned(), id):
			owner = "n2"
		}
		lines = append(lines, fmt.Sprintf("%s received=%d owner=%s claims=%v", id, n, owner, c.accs.claimsOf(id)))
		if n != want/int64(len(c.ids)) {
			lines = append(lines, c.postMortem(t, id, tag, a, b)...)
		}
	}
	t.Fatalf("%s: %d received, want %d\n%s", what, c.count(events.MessageReceived), want, strings.Join(lines, "\n"))
}

// postMortem collects durable facts about an account that missed an event, without touching the hot path:
// what the server holds, what the index holds, the runner's state, and whether a fast resync recovers it.
func (c *cluster) postMortem(t *testing.T, id, tag string, a, b *Manager) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := c.users[id]
	mid := "<" + tag + "-" + user + "@x>"
	var out []string
	if cl, err := imapc.Dial(ctx, imapc.Options{Addr: c.sim.Addr.IMAPPlain, TLS: imapc.TLSNone, Username: user, Password: "pw"}); err != nil {
		out = append(out, "  server: dial "+err.Error())
	} else {
		sel, err := cl.Select(ctx, "INBOX", true)
		uids, serr := cl.SearchMessageID(ctx, mid)
		out = append(out, fmt.Sprintf("  server: select=%+v err=%v search %s → %v err=%v", sel, err, mid, uids, serr))
		_ = cl.Logout(ctx)
	}
	var mb bson.M
	_ = c.db.Collection(index.Mailboxes).FindOne(ctx, bson.M{"accountId": id, "path": "INBOX"}).Decode(&mb)
	out = append(out, fmt.Sprintf("  index mailbox: uidNext=%v status=%v lastSyncAt=%v exists=%v", mb["uidNext"], mb["status"], mb["lastSyncAt"], mb["exists"]))
	nmsg, _ := c.db.Collection(index.Messages).CountDocuments(ctx, bson.M{"mailboxId": mb["_id"]})
	var entry bson.M
	_ = c.db.Collection(index.Messages).FindOne(ctx, bson.M{"mailboxId": mb["_id"], "messageIdHeader": mid}).Decode(&entry)
	out = append(out, fmt.Sprintf("  index messages=%d entry for %s: uid=%v", nmsg, mid, entry["uid"]))
	var acc bson.M
	_ = c.db.Collection(index.Accounts).FindOne(ctx, bson.M{"_id": id}).Decode(&acc)
	out = append(out, fmt.Sprintf("  account: state=%v owner=%v", acc["state"], acc["owner"]))
	for _, m := range []*Manager{a, b} {
		m.mu.Lock()
		o := m.held[id]
		m.mu.Unlock()
		if o == nil {
			continue
		}
		finished := false
		select {
		case <-o.done:
			finished = true
		default:
		}
		out = append(out, fmt.Sprintf("  runner on %s: epoch=%d finished=%v handoffTo=%q", m.NodeID, o.epoch, finished, o.handoffTo))
	}
	before, _ := c.db.Collection(events.Collection).CountDocuments(ctx, bson.M{"type": events.MessageReceived, "accountId": id})
	node := "n1"
	if slices.Contains(b.Owned(), id) {
		node = "n2"
	}
	msg, _ := json.Marshal(CommandMessage{AccountID: id, Command: Command{Cmd: CmdResync, Args: map[string]any{"mode": "fast"}}})
	_ = c.busA.PublishCore(bus.NodeSyncSubject(node), msg)
	time.Sleep(5 * time.Second)
	after, _ := c.db.Collection(events.Collection).CountDocuments(ctx, bson.M{"type": events.MessageReceived, "accountId": id})
	out = append(out, fmt.Sprintf("  fast resync probe on %s: received %d → %d", node, before, after))
	return out
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	slices.Sort(out)
	return out
}

// slowAccounts makes the placement tick slow on demand (a slow MongoDB, a long batch of claims).
type slowAccounts struct {
	*clusterAccounts
	slow gosync.Mutex // held while slow: Placeable blocks on it
}

func (s *slowAccounts) Placeable(ctx context.Context) ([]string, error) {
	locked := make(chan struct{})
	go func() { s.slow.Lock(); close(locked) }()
	select {
	case <-locked:
		s.slow.Unlock()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return s.clusterAccounts.Placeable(ctx)
}

// Lease renewal must not depend on the placement tick: with the tick blocked for three lease TTLs, every
// lease is still renewed and no account is stopped (P2.4: a busy tick once expired all leases of a node).
func TestIntegrationLeasesRenewDuringSlowTick(t *testing.T) {
	c := clusterSetup(t, 4)
	ctx := context.Background()
	if err := c.busA.RegisterNode(ctx, bus.NodeInfo{NodeID: "n1", Roles: []string{"sync"}}); err != nil {
		t.Fatal(err)
	}
	slow := &slowAccounts{clusterAccounts: c.accs}
	logA := &msgSink{}
	a := c.manager(c.busA, "n1", slog.New(logA))
	a.Accounts = slow
	stop := run(a)
	defer stop()
	waitFor(t, 30*time.Second, "accounts not owned", func() bool { return len(a.Owned()) == len(c.ids) })

	slow.slow.Lock() // the next tick blocks in Placeable
	time.Sleep(3 * a.LeaseTTL)
	for _, id := range c.ids {
		h, err := c.busA.LeaseHolder(ctx, LeasePrefix+id)
		if err != nil || h == nil {
			t.Fatalf("%s: lease gone while the tick was blocked (%v)", id, err)
		}
		if node, _ := leaseOwner(h.Value); node != "n1" {
			t.Fatalf("%s: held by %q", id, node)
		}
	}
	slow.slow.Unlock()
	if n := logA.count("account lease lost; stopping the account"); n != 0 {
		t.Fatalf("%d leases lost during a slow tick", n)
	}
	if got := len(a.Owned()); got != len(c.ids) {
		t.Fatalf("owned %d of %d after the slow tick", got, len(c.ids))
	}
}
