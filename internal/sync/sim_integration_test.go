//go:build integration

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

// simSetup is setup() against the in-process provider simulator (no CONDSTORE advertised).
func simSetup(t *testing.T) (*env, *simtest.Env, string) {
	t.Helper()
	sim := simtest.Start(t)
	user := fmt.Sprintf("u%d", time.Now().UnixNano())
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EB_TEST_IMAP_ADDR", sim.Addr.IMAPPlain)
	t.Setenv("EB_TEST_IMAP_PASSWORD", "pw")
	e := setup(t)
	e.accs.acc.IMAPTLS, e.accs.acc.IMAPUser, e.accs.acc.IMAPPass = imapc.TLSNone, user, "pw"
	e.imap = imapc.Options{Addr: sim.Addr.IMAPPlain, TLS: imapc.TLSNone, Username: user, Password: "pw"}
	return e, sim, user
}

func TestIntegrationSimulatorFullDiffMode(t *testing.T) {
	_ = os.Getenv
	e, sim, user := simSetup(t)
	ctx := context.Background()
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("old", "<old@x>"), nil, time.Now().Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	m := e.manager()
	m.Config.FullResync = time.Millisecond // no CONDSTORE: every pass diffs flags and UIDs
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	time.Sleep(300 * time.Millisecond)
	res, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("new", "<new@x>"), nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "new mail not reported", func() bool { return e.count(events.MessageReceived) == 1 })
	resync := func() {
		b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync}})
		_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	}
	if err := sim.Client.SetFlags(ctx, user, "INBOX", []uint32{res.UID}, []string{`\Seen`}, "add"); err != nil {
		t.Fatal(err)
	}
	resync()
	waitFor(t, 15*time.Second, "flag change not found by the full diff", func() bool { return e.count(events.MessageUpdated) >= 1 })
	if err := sim.Client.SetFlags(ctx, user, "INBOX", []uint32{res.UID}, []string{`\Deleted`}, "add"); err != nil {
		t.Fatal(err)
	}
	if err := sim.Client.Expunge(ctx, user, "INBOX", []uint32{res.UID}); err != nil {
		t.Fatal(err)
	}
	resync()
	waitFor(t, 15*time.Second, "deletion not found by the full diff", func() bool { return e.count(events.MessageDeleted) == 1 })
	if e.count(events.MessageReceived) != 1 {
		t.Fatal("old mail replayed")
	}
}

func TestIntegrationSimulatorFaults(t *testing.T) {
	e, sim, user := simSetup(t)
	ctx := context.Background()
	m := e.manager()
	m.Config.MaxIdle = time.Second // DONE is sent every second
	m.Config.DoneTimeout = 500 * time.Millisecond
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	// half-open connection: the server never answers DONE → mail_timeout → reconnect
	if err := sim.Client.SetIMAPFault(ctx, user, simulator.IMAPFault{NeverDone: true, Remaining: 1}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "half-open IDLE not detected", func() bool { return e.count(events.AccountConnectFailed) >= 1 })
	var doc bson.M
	_ = e.db.Collection(index.Accounts).FindOne(ctx, bson.M{"_id": e.accs.acc.ID}).Decode(&doc)
	if errs, _ := doc["recentErrors"].(bson.A); len(errs) == 0 {
		t.Fatal("timeline empty")
	}
	_ = sim.Client.ClearIMAPFault(ctx, user)
	// the account recovers on its own and new mail still arrives
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("after", "<after@x>"), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "no recovery after the half-open connection", func() bool { return e.count(events.MessageReceived) == 1 })
	_ = store.NewID
}

type memArchive struct {
	mu   gosync.Mutex
	keys map[string][]byte
}

func (a *memArchive) PutRaw(_ context.Context, org, acc, msgID string, at time.Time, raw []byte) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := fmt.Sprintf("archive/%s/%s/%04d/%02d/%s.eml", org, acc, at.Year(), at.Month(), msgID)
	a.keys[key] = raw
	return key, nil
}

func TestIntegrationSimulatorArchiveRaw(t *testing.T) {
	e, sim, user := simSetup(t)
	e.accs.acc.ArchiveRaw = true
	arch := &memArchive{keys: map[string][]byte{}}
	m := e.manager()
	m.Archive = arch
	ctx := context.Background()
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	time.Sleep(300 * time.Millisecond)
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("archived", "<arch@x>"), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "not reported", func() bool { return e.count(events.MessageReceived) == 1 })
	var entry bson.M
	if err := e.db.Collection(index.Messages).FindOne(ctx, bson.M{"messageIdHeader": "<arch@x>"}).Decode(&entry); err != nil {
		t.Fatal(err)
	}
	key, _ := entry["archiveRef"].(string)
	arch.mu.Lock()
	defer arch.mu.Unlock()
	if key == "" || !strings.Contains(string(arch.keys[key]), "Subject: archived") {
		t.Fatalf("archive ref %q, keys %d", key, len(arch.keys))
	}
}

func TestIntegrationSimulatorRecreateIdleFolder(t *testing.T) {
	e, sim, user := simSetup(t)
	ctx := context.Background()
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("pre", "<pre@x>"), nil, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	m := e.manager()
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	time.Sleep(500 * time.Millisecond) // idling on INBOX
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("pre-reset", "<prer@x>"), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := sim.Client.RecreateMailbox(ctx, user, "INBOX"); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw("post", "<post@x>"), nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync, Args: map[string]any{"mode": "fast"}}})
	_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	waitFor(t, 20*time.Second, "mailbox.reset missing after recreating the IDLE folder", func() bool { return e.count(events.MailboxReset) == 1 })
	if n := e.count(events.MessageReceived); n != 1 { // only "pre-reset", reported via IDLE before the reset
		t.Fatalf("reset re-index must be silent, got %d message.received", n)
	}
}
