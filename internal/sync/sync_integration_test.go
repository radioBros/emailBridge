//go:build integration

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/protolog"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/test/containers"
)

// testAccounts is an in-memory account source writing the S1 fields the engine relies on (owner, state).
type testAccounts struct {
	db  *store.DB
	acc Account
	mu  gosync.Mutex
	log []string
}

func (a *testAccounts) Placeable(context.Context) ([]string, error) { return []string{a.acc.ID}, nil }
func (a *testAccounts) Load(context.Context, string) (*Account, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.acc
	return &c, nil
}

func (a *testAccounts) Claimed(ctx context.Context, id, node string, epoch uint64) ([]Command, error) {
	_, err := a.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"owner": bson.M{"nodeId": node, "epoch": int64(epoch), "since": time.Now()}}})
	return nil, err
}

func (a *testAccounts) Transition(ctx context.Context, id string, epoch uint64, to string, _ *types.Reason) error {
	res, err := a.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": id, "owner.epoch": int64(epoch)}, bson.M{"$set": bson.M{"state": to}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return index.ErrFenced
	}
	a.mu.Lock()
	a.log = append(a.log, to)
	a.mu.Unlock()
	return nil
}

func (a *testAccounts) states() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.log...)
}

type env struct {
	db    *store.DB
	bus   *bus.Bus
	accs  *testAccounts
	ix    *index.Index
	imap  imapc.Options
	orgID string
}

func setup(t *testing.T) *env {
	t.Helper()
	addr, pass := os.Getenv("EB_TEST_IMAP_ADDR"), os.Getenv("EB_TEST_IMAP_PASSWORD")
	if addr == "" {
		if d, ok := containers.DovecotCompose(); ok {
			addr, pass = d.IMAPS, d.Password
		} else { // no compose stack: one Dovecot container shared by the package's tests
			d := containers.DovecotShared(t)
			addr, pass = d.IMAPS, d.Password
		}
	}
	if pass == "" {
		pass = "devpass"
	}
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_sync_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for i, m := range []store.Migration{index.Migration(1), events.Migration(2)} {
		_ = i
		if err := m.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	org, acc := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixAccount)
	user := fmt.Sprintf("sync%d", time.Now().UnixNano())
	if _, err := db.Collection(index.Accounts).InsertOne(ctx, bson.M{"_id": acc, "organizationId": org, "state": StateNew}); err != nil {
		t.Fatal(err)
	}
	accs := &testAccounts{db: db, acc: Account{ID: acc, OrganizationID: org, Email: user + "@test", State: StateNew,
		IMAPAddr: addr, IMAPTLS: imapc.TLSImplicit, IMAPUser: user, IMAPPass: pass, IgnoreCertErrors: true,
		Paths: []string{"*"}, NotifyFrom: time.Now().Add(-time.Hour)}}
	return &env{db: db, bus: b, accs: accs, ix: &index.Index{DB: db}, orgID: org,
		imap: imapc.Options{Addr: addr, TLS: imapc.TLSImplicit, InsecureSkipVerify: true, Username: user, Password: pass}}
}

func (e *env) manager() *Manager {
	return &Manager{NodeID: "n1", Bus: e.bus, Accounts: e.accs, Index: e.ix, Events: &events.Writer{DB: e.db},
		Config:   Config{ResyncDelay: 2 * time.Second, ReconnectMin: 200 * time.Millisecond, ReconnectMax: time.Second},
		LeaseTTL: 3 * time.Second, Tick: 200 * time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func (e *env) run(t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	m := e.manager()
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	return func() { cancel(); <-done }
}

func (e *env) count(typ string) int64 {
	n, _ := e.db.Collection(events.Collection).CountDocuments(context.Background(), bson.M{"type": typ})
	return n
}

func (e *env) eventsOf(typ string) []bson.M {
	cur, err := e.db.Collection(events.Collection).Find(context.Background(), bson.M{"type": typ})
	if err != nil {
		return nil
	}
	var out []bson.M
	_ = cur.All(context.Background(), &out)
	return out
}

func waitFor(t *testing.T, d time.Duration, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func raw(subject, id string) []byte {
	return []byte(fmt.Sprintf("From: A <a@example.test>\r\nTo: B <b@example.test>\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\n\r\nbody %s\r\n", subject, id, time.Now().Format(time.RFC1123Z), subject))
}

func TestIntegrationSyncLifecycle(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	// two old messages: indexed silently by the baseline
	for i := 0; i < 2; i++ {
		if _, err := c.Append(ctx, "INBOX", nil, time.Now().Add(-48*time.Hour), raw(fmt.Sprintf("old %d", i), fmt.Sprintf("<old%d@x>", i))); err != nil {
			t.Fatal(err)
		}
	}
	stop := e.run(t)
	waitFor(t, 30*time.Second, "account never initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	if e.count(events.MessageReceived) != 0 {
		t.Fatal("old mail must be indexed silently (no replay)")
	}
	waitFor(t, 10*time.Second, "not connected", func() bool {
		var doc bson.M
		_ = e.db.Collection(index.Accounts).FindOne(ctx, bson.M{"_id": e.accs.acc.ID}).Decode(&doc)
		return doc["state"] == StateConnected
	})
	time.Sleep(500 * time.Millisecond) // IDLE started
	// new mail arrives → message.received via IDLE
	start := time.Now()
	if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw("fresh", "<fresh@x>")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "new mail not reported", func() bool { return e.count(events.MessageReceived) == 1 })
	t.Logf("APPEND → message.received in %v", time.Since(start))
	ev := e.eventsOf(events.MessageReceived)[0]
	var data types.MessageReceived
	if err := json.Unmarshal([]byte(ev["data"].(string)), &data); err != nil {
		t.Fatal(err)
	}
	if data.Subject != "fresh" || data.MessageIDHeader != "<fresh@x>" || data.From == nil || data.From.Address != "a@example.test" || data.Mailbox.Path != "INBOX" || data.UID == 0 {
		t.Fatalf("payload %+v", data)
	}
	// flag change → message.updated
	if _, err := c.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	if err := c.StoreFlags(ctx, []uint32{data.UID}, true, []string{`\Flagged`}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "flag change not reported", func() bool { return e.count(events.MessageUpdated) >= 1 })
	// delete → message.deleted
	if err := c.StoreFlags(ctx, []uint32{data.UID}, true, []string{`\Deleted`}); err != nil {
		t.Fatal(err)
	}
	if err := c.Expunge(ctx, []uint32{data.UID}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "deletion not reported", func() bool { return e.count(events.MessageDeleted) == 1 })
	// fencing: another owner takes the account in the database → this runner's writes fail; the manager
	// restarts it with a fresh lease and nothing is lost or duplicated
	if _, err := e.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": e.accs.acc.ID}, bson.M{"$set": bson.M{"owner.epoch": int64(999999)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw("after fence", "<fence@x>")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 40*time.Second, "mail after fencing not reported", func() bool { return e.count(events.MessageReceived) == 2 })
	stop()
	// restart: the index persists, so nothing is reported again
	stop2 := e.run(t)
	if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw("after restart", "<restart@x>")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "mail after restart not reported", func() bool { return e.count(events.MessageReceived) == 3 })
	time.Sleep(time.Second)
	stop2()
	if n := e.count(events.MessageReceived); n != 3 {
		t.Fatalf("received events %d, want 3 (no replay after restart)", n)
	}
	if e.count(events.AccountInitialized) != 1 {
		t.Fatal("initialised twice")
	}
	t.Logf("states: %v", e.accs.states())
	// cleanup the test mailbox
	if all, err := c.UIDs(ctx); err == nil && len(all) > 0 {
		_ = c.StoreFlags(ctx, all, true, []string{`\Deleted`})
		_ = c.Expunge(ctx, all)
	}
}

func TestIntegrationAuthFailure(t *testing.T) {
	e := setup(t)
	e.accs.acc.IMAPUser = "" // Dovecot refuses an empty login
	e.accs.acc.IMAPPass = "wrong"
	stop := e.run(t)
	defer stop()
	waitFor(t, 20*time.Second, "auth failure not reported", func() bool { return e.count(events.AccountAuthFailed) == 1 })
	var doc bson.M
	_ = e.db.Collection(index.Accounts).FindOne(context.Background(), bson.M{"_id": e.accs.acc.ID}).Decode(&doc)
	if doc["authFailingSince"] == nil {
		t.Fatal("authFailingSince not set")
	}
	if errs, _ := doc["recentErrors"].(bson.A); len(errs) == 0 {
		t.Fatal("error timeline empty")
	}
}

type fixedNeeds struct{ n Needs }

func (f fixedNeeds) Needs(context.Context, string) (Needs, error) { return f.n, nil }

func TestIntegrationFoldersCommandsAndPayload(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Create(ctx, "Work"); err != nil {
		t.Fatal(err)
	}
	m := e.manager()
	m.Needs = fixedNeeds{Needs{Headers: []string{"x-custom"}, Text: true, TextMax: 1000, Attachments: true}}
	m.Config.ResyncDelay = time.Hour // passes only through commands in this test
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	resync := func() {
		b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync, At: time.Now()}})
		if err := e.bus.PublishCore(bus.NodeSyncSubject("n1"), b); err != nil {
			t.Fatal(err)
		}
	}
	// a message in a non-IDLE folder, found by a resync command, with the payload the organisation needs
	msg := "From: A <a@example.test>\r\nTo: B <b@example.test>\r\nSubject: with parts\r\nMessage-ID: <parts@x>\r\nX-Custom: yes\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello text\r\n" +
		"--b\r\nContent-Type: application/pdf; name=a.pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\n\r\nPDF\r\n--b--\r\n"
	if _, err := c.Append(ctx, "Work", nil, time.Now(), []byte(msg)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	resync()
	waitFor(t, 15*time.Second, "message in Work not reported after resync", func() bool { return e.count(events.MessageReceived) == 1 })
	var data types.MessageReceived
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageReceived)[0]["data"].(string)), &data)
	if data.Mailbox.Path != "Work" || data.Headers["x-custom"] == nil || data.Text == nil || data.Text.Plain == "" ||
		len(data.Attachments) != 1 || data.Attachments[0].Filename != "a.pdf" {
		t.Fatalf("payload %+v text %+v", data, data.Text)
	}
	// a new folder → mailbox.created; delete + recreate → mailbox.reset (new UIDVALIDITY)
	if err := c.Create(ctx, "Later"); err != nil {
		t.Fatal(err)
	}
	resync()
	waitFor(t, 15*time.Second, "mailbox.created missing", func() bool { return e.count(events.MailboxCreated) == 1 })
	if err := c.Delete(ctx, "Work"); err != nil {
		t.Fatal(err)
	}
	resync()
	waitFor(t, 15*time.Second, "mailbox.deleted missing", func() bool { return e.count(events.MailboxDeleted) == 1 })
	// reconnect command
	b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdReconnect}})
	_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	waitFor(t, 15*time.Second, "reconnect not executed", func() bool {
		n := 0
		for _, s := range e.accs.states() {
			if s == StateConnecting {
				n++
			}
		}
		return n >= 2
	})
	if len(m.Owned()) != 1 || m.Stats().Passes == 0 {
		t.Fatalf("owned %v stats %+v", m.Owned(), m.Stats())
	}
	_ = c.Delete(ctx, "Later")
}

func TestIntegrationUIDValidityReset(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.Create(ctx, "Proj")
	_, _ = c.Append(ctx, "Proj", nil, time.Now(), raw("p1", "<p1@x>"))
	m := e.manager()
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	// recreate the folder between passes: same path, new UIDVALIDITY
	_ = c.Delete(ctx, "Proj")
	_ = c.Create(ctx, "Proj")
	_, _ = c.Append(ctx, "Proj", nil, time.Now(), raw("p2", "<p2@x>"))
	b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync}})
	_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	waitFor(t, 15*time.Second, "mailbox.reset missing", func() bool { return e.count(events.MailboxReset) == 1 })
	if e.count(events.MessageReceived) != 0 {
		t.Fatal("a reset folder is re-indexed silently")
	}
	_ = c.Delete(ctx, "Proj")
}

func TestIntegrationConnectFailureAndDisable(t *testing.T) {
	e := setup(t)
	e.accs.acc.IMAPAddr = "127.0.0.1:1" // refused
	stop := e.run(t)
	waitFor(t, 20*time.Second, "connect failure not reported", func() bool { return e.count(events.AccountConnectFailed) == 1 })
	stop()
	// auth failing for longer than the disable window → disabled, runner stops
	e2 := setup(t)
	e2.accs.acc.IMAPUser, e2.accs.acc.IMAPPass = "", "wrong"
	past := time.Now().Add(-100 * time.Hour)
	e2.accs.acc.AuthFailingSince = &past
	stop2 := e2.run(t)
	defer stop2()
	waitFor(t, 20*time.Second, "not disabled", func() bool {
		for _, s := range e2.accs.states() {
			if s == StateDisabled {
				return true
			}
		}
		return false
	})
}

type lineSink struct {
	mu    gosync.Mutex
	lines []string
}

func (l *lineSink) Enabled(context.Context, slog.Level) bool { return true }
func (l *lineSink) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "line" {
			l.lines = append(l.lines, a.Value.String())
		}
		return true
	})
	return nil
}
func (l *lineSink) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *lineSink) WithGroup(string) slog.Handler      { return l }

func TestIntegrationProtocolLogs(t *testing.T) {
	e := setup(t)
	e.accs.acc.ProtocolLogs = true
	sink := &lineSink{}
	m := e.manager()
	m.Tap = &protolog.Recorder{Log: slog.New(sink)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	cancel()
	<-done
	sink.mu.Lock()
	defer sink.mu.Unlock()
	all := strings.Join(sink.lines, "\n")
	if !strings.Contains(all, "SELECT") || strings.Contains(all, e.accs.acc.IMAPPass) {
		t.Fatalf("transcript must be recorded with credentials masked:\n%s", all)
	}
}

func TestIntegrationCondstoreDeletionAuthResultsAndRecovery(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	_ = c.Create(ctx, "Arch")
	past := time.Now().Add(-time.Hour)
	e.accs.acc.AuthFailingSince = &past // failing before: a successful login reports auth_succeeded
	m := e.manager()
	m.Needs = fixedNeeds{Needs{AuthResults: "header", TrustedAuth: []string{"mx.trusted.test"}}}
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	if e.count(events.AccountAuthSucceeded) != 1 {
		t.Fatal("auth_succeeded missing after earlier failures")
	}
	msg := "From: A <a@example.test>\r\nTo: B <b@example.test>\r\nSubject: auth\r\nMessage-ID: <auth@x>\r\n" +
		"Authentication-Results: mx.trusted.test; spf=pass smtp.mailfrom=example.test; dkim=pass header.d=example.test header.s=s1; dmarc=pass header.from=example.test\r\n\r\nbody\r\n"
	uid, err := c.Append(ctx, "Arch", nil, time.Now(), []byte(msg))
	if err != nil {
		t.Fatal(err)
	}
	cmd := func(name string) {
		c := Command{Cmd: name}
		if name == CmdResync { // fast: this test covers the CONDSTORE path (EXISTS mismatch → UID diff), not the full diff
			c.Args = map[string]any{"mode": "fast"}
		}
		b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: c})
		_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	}
	cmd(CmdResync)
	waitFor(t, 15*time.Second, "not reported", func() bool { return e.count(events.MessageReceived) == 1 })
	var data types.MessageReceived
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageReceived)[0]["data"].(string)), &data)
	if data.AuthResults == nil || data.AuthResults.SPF == nil || data.AuthResults.SPF.Result != "pass" || len(data.AuthResults.DKIM) == 0 {
		t.Fatalf("auth results %+v", data.AuthResults)
	}
	// a deletion in a non-IDLE CONDSTORE folder is found by the EXISTS mismatch + UID diff
	if _, err := c.Select(ctx, "Arch", false); err != nil {
		t.Fatal(err)
	}
	_ = c.StoreFlags(ctx, []uint32{uid}, true, []string{`\Deleted`})
	if err := c.Expunge(ctx, []uint32{uid}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	cmd(CmdResync)
	waitFor(t, 15*time.Second, "deletion in Arch not found", func() bool { return e.count(events.MessageDeleted) == 1 })
	// configChanged reloads the account and reconnects
	cmd(CmdConfigChanged)
	waitFor(t, 15*time.Second, "configChanged not executed", func() bool {
		n := 0
		for _, s := range e.accs.states() {
			if s == StateConnecting {
				n++
			}
		}
		return n >= 2
	})
	_ = c.Delete(ctx, "Arch")
}

func TestIntegrationPollLoopWithoutIdleFolder(t *testing.T) {
	e := setup(t)
	e.accs.acc.IdlePaths = []string{"Nope"} // no IDLE folder → poll loop
	m := e.manager()
	m.Config.NoIdlePoll = 200 * time.Millisecond
	m.Config.ResyncDelay = 500 * time.Millisecond
	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })
	c, err := imapc.Dial(context.Background(), e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Append(context.Background(), "INBOX", nil, time.Now(), raw("polled", "<polled@x>")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "polled mail not reported", func() bool { return e.count(events.MessageReceived) == 1 })
	b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync}})
	_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	time.Sleep(time.Second)
}

func TestIntegrationModule(t *testing.T) {
	e := setup(t)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Provide(a, e.db)
	app.Provide(a, e.bus)
	app.Provide(a, &events.Writer{DB: e.db})
	m := &Module{Accounts: e.accs, LeaseTTL: 3 * time.Second}
	if m.Name() != "sync" || len(m.Roles()) != 1 {
		t.Fatal("identity")
	}
	if err := (&Module{}).Start(context.Background(), a); err == nil {
		t.Fatal("no account source must fail")
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "module did not run the account", func() bool { return e.count(events.AccountInitialized) == 1 })
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	var doc bson.M
	_ = e.db.Collection(index.Accounts).FindOne(context.Background(), bson.M{"_id": e.accs.acc.ID}).Decode(&doc)
	if doc["state"] != StateDisconnected {
		t.Fatalf("state after stop: %v (want disconnected before the lease is released)", doc["state"])
	}
}

func TestIntegrationCopyToSent(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	cp := &SentCopier{Accounts: e.accs}
	folder, uid, err := cp.CopyToSent(ctx, e.accs.acc.ID, "", raw("sent copy", "<sent@x>"), time.Now().Add(-time.Minute))
	if err != nil || folder == "" {
		t.Fatalf("%s %d %v", folder, uid, err)
	}
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Select(ctx, folder, true); err != nil {
		t.Fatal(err)
	}
	found, err := c.SearchMessageID(ctx, "<sent@x>")
	if err != nil || len(found) != 1 {
		t.Fatalf("copy not in %s: %v %v", folder, found, err)
	}
	msgs, _ := c.FetchUIDs(ctx, found, imapc.FetchOptions{Flags: true})
	if len(msgs) != 1 || !strings.Contains(strings.Join(msgs[0].Flags, " "), `\Seen`) {
		t.Fatalf("flags %+v", msgs)
	}
	if _, _, err := cp.CopyToSent(ctx, e.accs.acc.ID, "Does/Not/Exist", raw("x", "<x@x>"), time.Now()); err == nil {
		t.Fatal("append to a missing folder must fail")
	}
}

// With sync.qresync on, a resync of a non-IDLE folder runs SELECT … (QRESYNC …): a flag change and an expunge
// made by another client are reported from that one answer (S5 §4 qresync mode).
func TestIntegrationQResyncMode(t *testing.T) {
	e := setup(t)
	e.accs.acc.QResync = true
	e.accs.acc.ProtocolLogs = true
	sink := &lineSink{}
	ctx := context.Background()
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if !c.Has("QRESYNC") {
		t.Skip("server does not offer QRESYNC")
	}
	_ = c.Create(ctx, "QR")
	var uids []uint32
	for i := range 2 {
		uid, err := c.Append(ctx, "QR", nil, time.Now(), raw(fmt.Sprintf("qr %d", i), fmt.Sprintf("<qr-%d@x>", i)))
		if err != nil {
			t.Fatal(err)
		}
		uids = append(uids, uid)
	}
	m := e.manager()
	m.Tap = &protolog.Recorder{Log: slog.New(sink)}
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	var stopOnce gosync.Once
	stop := func() { stopOnce.Do(func() { cancel(); <-done }) }
	defer stop()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })

	if _, err := c.Select(ctx, "QR", false); err != nil {
		t.Fatal(err)
	}
	if err := c.StoreFlags(ctx, uids[:1], true, []string{`\Flagged`}); err != nil {
		t.Fatal(err)
	}
	_ = c.StoreFlags(ctx, uids[1:], true, []string{`\Deleted`})
	if err := c.Expunge(ctx, uids[1:]); err != nil {
		t.Fatal(err)
	}
	// mode fast: a full resync deliberately takes the full-diff path as a safety net
	b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync, Args: map[string]any{"mode": "fast"}}})
	_ = e.bus.PublishCore(bus.NodeSyncSubject("n1"), b)
	waitFor(t, 20*time.Second, "flag change not reported", func() bool { return e.count(events.MessageUpdated) == 1 })
	waitFor(t, 20*time.Second, "deletion not reported", func() bool { return e.count(events.MessageDeleted) == 1 })

	stop() // the transcript is flushed when the connection closes
	sink.mu.Lock()
	all := strings.Join(sink.lines, "\n")
	sink.mu.Unlock()
	if !strings.Contains(all, "ENABLE QRESYNC") || !strings.Contains(all, "(QRESYNC (") {
		t.Fatalf("the qresync path did not run:\n%s", all)
	}
	_ = c.Delete(ctx, "QR")
}

// P4.2 payload options: web-safe HTML (scripts and handlers removed) and parsed calendar parts come from the
// same fetch as the text, only when an endpoint asks for them.
func TestIntegrationWebSafeHTMLAndCalendar(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Create(ctx, "Invites"); err != nil {
		t.Fatal(err)
	}
	m := e.manager()
	m.Needs = fixedNeeds{Needs{WebSafeHTML: true, CalendarEvents: true}}
	m.Config.ResyncDelay = time.Hour
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })

	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//test//x//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:evt-42@example.test\r\n" +
		"SEQUENCE:0\r\nDTSTAMP:20261026T090000Z\r\nDTSTART:20261027T100000Z\r\nDTEND:20261027T110000Z\r\nSUMMARY:Planning\r\n" +
		"ORGANIZER:mailto:a@example.test\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	msg := "From: A <a@example.test>\r\nTo: B <b@example.test>\r\nSubject: invite\r\nMessage-ID: <invite@x>\r\n" +
		"Date: Mon, 26 Oct 2026 09:00:00 +0000\r\n" +
		"Content-Type: multipart/alternative; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nplain\r\n" +
		"--b\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<p onclick=\"steal()\">Hello</p><script>alert(1)</script>" +
		"<a href=\"https://example.test/x\">link</a>\r\n--b\r\nContent-Type: text/calendar; method=REQUEST; charset=utf-8\r\n\r\n" + ics + "--b--\r\n"
	if _, err := c.Append(ctx, "Invites", nil, time.Now(), []byte(msg)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	b, _ := json.Marshal(CommandMessage{AccountID: e.accs.acc.ID, Command: Command{Cmd: CmdResync, At: time.Now()}})
	if err := e.bus.PublishCore(bus.NodeSyncSubject("n1"), b); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 15*time.Second, "message not reported", func() bool { return e.count(events.MessageReceived) == 1 })
	var data types.MessageReceived
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageReceived)[0]["data"].(string)), &data)
	if !strings.Contains(data.WebSafeHTML, "Hello") || strings.Contains(data.WebSafeHTML, "script") ||
		strings.Contains(data.WebSafeHTML, "onclick") || !strings.Contains(data.WebSafeHTML, "noopener") {
		t.Fatalf("webSafeHtml = %q", data.WebSafeHTML)
	}
	if len(data.CalendarEvents) != 1 || data.CalendarEvents[0].UID != "evt-42@example.test" || data.CalendarEvents[0].Summary != "Planning" {
		t.Fatalf("calendarEvents = %+v", data.CalendarEvents)
	}
	if data.Text != nil {
		t.Fatal("text was not asked for and must not be in the payload")
	}
	// The index row carries the envelope summary and the thread ID (P4.4 message browser).
	var row index.Message
	if err := e.db.Collection(index.Messages).FindOne(ctx, bson.M{"_id": data.MessageID}).Decode(&row); err != nil {
		t.Fatal(err)
	}
	if row.Subject != "invite" || row.From == nil || row.From.Address != "a@example.test" || len(row.To) != 1 ||
		row.ThreadID == "" || row.ThreadID != data.ThreadID || row.Date == nil {
		t.Fatalf("index summary = %+v", row)
	}
}

// P5.4: with inbound.bounceDetection on, a DSN among new mail enriches message.received with `bounce` and
// emits message.bounced linked to the outbound message; ordinary mail is not fetched twice.
func TestIntegrationBounceDetection(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	raw, err := os.ReadFile(filepath.Join("..", "bounce", "testdata", "corpus", "dsn_hard_001.eml"))
	if err != nil {
		t.Fatal(err)
	}
	e.accs.acc.BounceDetection = true
	var looked []string
	m := e.manager()
	m.Outbound = func(_ context.Context, org, outboundID, msgID string) (string, error) {
		looked = append(looked, outboundID+"|"+msgID)
		return "out_matched", nil
	}
	sup := &fakeSuppressor{}
	m.Suppress = sup
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })

	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "message.bounced missing", func() bool { return e.count(events.MessageBounced) == 1 })
	var rec types.MessageReceived
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageReceived)[0]["data"].(string)), &rec)
	if rec.Bounce == nil || rec.Bounce.Kind != "dsn_bounce" || rec.Bounce.OutboundID != "out_matched" ||
		rec.Bounce.OriginalMessageID == "" || len(rec.Bounce.Recipients) != 1 {
		t.Fatalf("message.received bounce = %+v", rec.Bounce)
	}
	var b types.MessageBounced
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageBounced)[0]["data"].(string)), &b)
	if b.Recipient != "user01@example.com" || b.BounceType != "hard" || b.Status != "5.1.1" || b.OutboundID != "out_matched" {
		t.Fatalf("message.bounced = %+v", b)
	}
	if len(looked) == 0 {
		t.Fatal("the outbound lookup was not consulted")
	}
	if got := sup.list(); len(got) != 1 || got[0] != "bounce:user01@example.com" {
		t.Fatalf("suppression calls = %v, want the hard-bounced recipient once", got)
	}
}

type fakeSuppressor struct {
	mu    gosync.Mutex
	calls []string
}

func (f *fakeSuppressor) AddBounce(_ context.Context, _, address, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "bounce:"+address)
	return nil
}

func (f *fakeSuppressor) AddComplaint(_ context.Context, _, address, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "complaint:"+address)
	return nil
}

func (f *fakeSuppressor) list() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

type fakeScorer struct {
	mu   gosync.Mutex
	orgs []string
	size int
}

func (f *fakeScorer) Score(_ context.Context, org string, raw []byte) (*types.SpamInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgs = append(f.orgs, org)
	f.size = len(raw)
	return &types.SpamInfo{Score: 9.2, Action: "reject", Symbols: map[string]float64{"GTUBE": 9}}, nil
}

// Spam scoring (P8.3) runs only for accounts with integrations.rspamd on and lands on message.received.spam.
func TestIntegrationSpamScoring(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.accs.acc.SpamScoring = true
	sc := &fakeScorer{}
	m := e.manager()
	m.Scorer = sc
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })

	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	raw := []byte("From: a@example.test\r\nTo: b@example.test\r\nSubject: spam?\r\nMessage-ID: <spam-1@example.test>\r\n\r\nbuy now\r\n")
	if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "message.received missing", func() bool { return e.count(events.MessageReceived) == 1 })
	var rec types.MessageReceived
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageReceived)[0]["data"].(string)), &rec)
	if rec.Spam == nil || rec.Spam.Score != 9.2 || rec.Spam.Action != "reject" {
		t.Fatalf("message.received spam = %+v", rec.Spam)
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if len(sc.orgs) != 1 || sc.orgs[0] != e.accs.acc.OrganizationID || sc.size < len(raw)-4 {
		t.Fatalf("scorer calls %v size %d", sc.orgs, sc.size)
	}
}
