package graph_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/store"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

func TestMigrationAndMemoryState(t *testing.T) {
	m := graph.Migration(35)
	if m.Number != 35 || m.Name == "" || m.Up == nil {
		t.Fatalf("%+v", m)
	}
	ms := &graph.MemoryState{}
	acc := &graph.Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	st, err := ms.Load(context.Background(), acc)
	if err != nil || st.UIDValidity == 0 || st.AccountID != acc.ID {
		t.Fatalf("%v %+v", err, st)
	}
	st.ClientState = "x"
	_ = ms.Save(context.Background(), st)
	st2, _ := ms.Load(context.Background(), acc)
	if st2.ClientState != "x" {
		t.Fatal(st2.ClientState)
	}
}

func TestModuleBasics(t *testing.T) {
	m := &graph.Module{BaseURL: "http://127.0.0.1:9", Log: slog.Default()}
	if m.Name() != "providers.graph" {
		t.Fatal(m.Name())
	}
	if len(m.Roles()) != 3 {
		t.Fatal(m.Roles())
	}
	a := app.New(app.Options{Log: slog.Default(), Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n1"})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Client() == nil || m.Sender() == nil {
		t.Fatal("nil client/sender")
	}
	r := m.NewRunner(graph.StaticToken{Token: "t"}, &graph.MemorySink{})
	if r == nil || r.Client == nil {
		t.Fatal("runner")
	}
	if m.Callback() != nil {
		t.Fatal("no callback without API role")
	}
	_ = m.Stop(context.Background())

	m2 := &graph.Module{BaseURL: "http://127.0.0.1:9", Log: slog.Default()}
	// Start without API server → RoleAPI path needs Server; sync-only already covered.
	_ = m2
}

func TestSenderAndMoveDelete(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")
	cli := &graph.Client{BaseURL: sim.URL()}
	s := &graph.Sender{Client: cli, Tokens: graph.StaticToken{Token: "tok"}}
	id, conv, err := s.Send(context.Background(), "acc", []byte("From: u@ex.com\r\nTo: x@ex.com\r\nSubject: S\r\n\r\nb"))
	if err != nil || id == "" {
		t.Fatalf("%v %s %s", err, id, conv)
	}
	if _, _, err := (&graph.Sender{}).Send(context.Background(), "a", []byte("x")); err == nil {
		t.Fatal("want err")
	}
	if _, _, err := s.Send(context.Background(), "a", nil); err == nil {
		t.Fatal("want empty err")
	}

	msgID, _, _ := sim.PutMessage("u@ex.com", rawMsg(), "inbox")
	sent := sim.FolderID("u@ex.com", "sentitems")
	if err := cli.Move(context.Background(), "tok", msgID, sent); err != nil {
		t.Fatal(err)
	}
	msgID2, _, _ := sim.PutMessage("u@ex.com", rawMsg(), "inbox")
	if err := cli.Delete(context.Background(), "tok", msgID2); err != nil {
		t.Fatal(err)
	}
}

func TestClientErrorsAndAuth(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	cli := &graph.Client{BaseURL: sim.URL()}
	if _, err := cli.ListFolders(context.Background(), "bad"); err == nil {
		t.Fatal("want auth err")
	}
	sim.SetToken("u@ex.com", "tok")
	if _, err := cli.GetMessage(context.Background(), "tok", "missing"); err == nil {
		t.Fatal("want not found")
	}
	if _, err := cli.GetMIME(context.Background(), "tok", "missing"); err == nil {
		t.Fatal("want mime not found")
	}

	creds := &graph.ClientCredentials{TokenURL: sim.URL() + "/token", ClientID: "c", Secret: "s"}
	if _, _, err := creds.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&graph.ClientCredentials{}).Token(context.Background()); err == nil {
		t.Fatal("want validation err")
	}
	// bad token endpoint
	bad := &graph.ClientCredentials{TokenURL: sim.URL() + "/nope", ClientID: "c", Secret: "s"}
	if _, _, err := bad.Token(context.Background()); err == nil {
		t.Fatal("want fail")
	}
}

func TestPayloadHelpers(t *testing.T) {
	acc := graph.Account{ID: "a", OrganizationID: "o"}
	raw := []byte("From: Name <a@ex.com>\r\nTo: b@ex.com, c@ex.com\r\nSubject: S\r\nMessage-ID: <1@ex>\r\n\r\n")
	m := graph.Message{ID: "g1", ConversationID: "c1", Received: time.Now().UTC()}
	d := graph.BuildReceived(acc, "mbx", "INBOX", `\Inbox`, 1, 1, m, raw, graph.Needs{Headers: []string{"*"}})
	if d.Headers == nil || d.From == nil || d.From.Address != "a@ex.com" {
		t.Fatalf("%+v", d)
	}
	d2 := graph.BuildReceived(acc, "mbx", "INBOX", `\Inbox`, 1, 1, m, raw, graph.Needs{Headers: []string{"subject"}})
	if len(d2.Headers["subject"]) == 0 {
		t.Fatal(d2.Headers)
	}
	imap := graph.IMAPShapedReceived(acc, "mbx", "INBOX", `\Inbox`, 1, 1, raw)
	if imap.From == nil {
		t.Fatal("from")
	}
}

func TestCallbackNudgeAndLookup(t *testing.T) {
	var nudged string
	h := &graph.CallbackHandler{
		Nudge: nudgeFn(func(ctx context.Context, id string) error {
			nudged = id
			return nil
		}),
	}
	st := &graph.SyncState{AccountID: "acc1", SubscriptionID: "sub1", ClientState: "secret"}
	h.Register(st)
	h.Register(nil)
	body := `{"value":[{"subscriptionId":"sub1","clientState":"secret","changeType":"created"}]}`
	req := httptest.NewRequest(http.MethodPost, "/callbacks/graph", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || nudged != "acc1" {
		t.Fatalf("%d nudged=%q", rec.Code, nudged)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/callbacks/graph", nil)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatal(rec2.Code)
	}
	// lookup by clientState only
	h2 := &graph.CallbackHandler{}
	h2.Register(&graph.SyncState{SubscriptionID: "s2", ClientState: "cs"})
	body2 := `{"value":[{"subscriptionId":"unknown","clientState":"cs","changeType":"updated"}]}`
	req3 := httptest.NewRequest(http.MethodPost, "/callbacks/graph", strings.NewReader(body2))
	rec3 := httptest.NewRecorder()
	h2.ServeHTTP(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("clientState lookup %d", rec3.Code)
	}
}

type nudgeFn func(ctx context.Context, id string) error

func (f nudgeFn) NudgeGraph(ctx context.Context, id string) error { return f(ctx, id) }

func TestSyncNilClientAndPaths(t *testing.T) {
	r := &graph.Runner{Tokens: graph.StaticToken{Token: "t"}, State: &graph.SyncState{}}
	if err := r.SyncOnce(context.Background(), graph.Account{ID: "a"}); err == nil {
		t.Fatal("want nil client err")
	}

	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")
	sink := &graph.MemorySink{}
	r = &graph.Runner{
		Client: &graph.Client{BaseURL: sim.URL()},
		Tokens: graph.StaticToken{Token: "tok"},
		Sink:   sink,
		State:  &graph.SyncState{UIDValidity: 1},
		Needs:  graph.Needs{Headers: []string{"from"}},
	}
	acc := graph.Account{
		ID: "acc", OrganizationID: "org", Email: "u@ex.com", Paths: []string{"Sent Items"},
		UPN: "u@ex.com", NotifyFrom: time.Now().UTC().Add(24 * time.Hour),
	}
	sim.PutMessage("u@ex.com", rawMsg(), "sentitems")
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	// second pass with future NotifyFrom skips emit
	sim.PutMessage("u@ex.com", append(rawMsg(), []byte("2")...), "sentitems")
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
}

func TestFoldersWatched(t *testing.T) {
	if graph.PathForWellKnown("nope") != "" {
		t.Fatal("want empty")
	}
	if graph.SpecialUseFor("INBOX") != `\Inbox` {
		t.Fatal(graph.SpecialUseFor("INBOX"))
	}
	path, su := graph.MailboxPath("", "")
	if path != "INBOX" || su != `\Inbox` {
		t.Fatalf("%s %s", path, su)
	}
}
