package graph_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

func rawMsg() []byte {
	return []byte("From: alice@ex.com\r\nTo: bob@ex.com\r\nCc: carol@ex.com\r\n" +
		"Subject: Hello Graph\r\nMessage-ID: <hello@ex.com>\r\n" +
		"Date: Mon, 26 Oct 2026 12:00:00 +0000\r\n" +
		"In-Reply-To: <prev@ex.com>\r\nReferences: <prev@ex.com>\r\n\r\nbody text")
}

func TestSamePayload_graphVsIMAPShaped(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("alice@ex.com", "tok")

	acc := graph.Account{
		ID: "acc_1", OrganizationID: "org_1", Email: "alice@ex.com",
		Paths: []string{"INBOX"},
	}
	sink := &graph.MemorySink{}
	cli := &graph.Client{BaseURL: sim.URL()}
	r := &graph.Runner{
		Client: cli,
		Tokens: graph.StaticToken{Token: "tok", Username: "alice@ex.com"},
		Sink:   sink,
		Needs:  graph.Needs{},
		State:  &graph.SyncState{UIDValidity: 1},
	}
	ctx := context.Background()

	// Baseline (empty) establishes delta link without events.
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if len(sink.ReceivedOf()) != 0 {
		t.Fatalf("baseline emitted %d", len(sink.ReceivedOf()))
	}

	sim.PutMessage("alice@ex.com", rawMsg(), "inbox")
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	got := sink.ReceivedOf()
	if len(got) != 1 {
		t.Fatalf("want 1 received, got %d (%+v)", len(got), sink.Events)
	}
	g := got[0]

	imap := graph.IMAPShapedReceived(acc, g.Mailbox.MailboxID, "INBOX", `\Inbox`, g.UID, g.UIDValidity, rawMsg())
	// Compare fields that must match across providers (P6 gate).
	if g.Subject != imap.Subject {
		t.Fatalf("subject %q vs %q", g.Subject, imap.Subject)
	}
	if g.MessageIDHeader != imap.MessageIDHeader {
		t.Fatalf("mid %q vs %q", g.MessageIDHeader, imap.MessageIDHeader)
	}
	if g.InReplyTo != imap.InReplyTo {
		t.Fatalf("irt %q vs %q", g.InReplyTo, imap.InReplyTo)
	}
	if g.From == nil || imap.From == nil || g.From.Address != imap.From.Address {
		t.Fatalf("from %+v vs %+v", g.From, imap.From)
	}
	if len(g.To) != 1 || g.To[0].Address != "bob@ex.com" {
		t.Fatalf("to %+v", g.To)
	}
	if len(g.Cc) != 1 || g.Cc[0].Address != "carol@ex.com" {
		t.Fatalf("cc %+v", g.Cc)
	}
	if g.Mailbox.Path != "INBOX" || g.Mailbox.SpecialUse != `\Inbox` {
		t.Fatalf("mailbox %+v", g.Mailbox)
	}
	if g.EmailID == "" || g.ThreadID == "" {
		t.Fatalf("provider ids empty: %s %s", g.EmailID, g.ThreadID)
	}
	if g.Labels == nil {
		t.Fatal("labels nil")
	}
	// Provider-specific fields differ by design.
	if g.EmailID == imap.EmailID && imap.EmailID != "" {
		t.Fatal("emailId should be Graph id, not IMAP-shaped empty")
	}
}

func TestDeltaReset_emitsMailboxReset(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")
	sim.PutMessage("u@ex.com", rawMsg(), "inbox")

	acc := graph.Account{ID: "acc_2", OrganizationID: "org_1", Email: "u@ex.com", Paths: []string{"*"}}
	sink := &graph.MemorySink{}
	r := &graph.Runner{
		Client: &graph.Client{BaseURL: sim.URL()},
		Tokens: graph.StaticToken{Token: "tok"},
		Sink:   sink,
		State:  &graph.SyncState{UIDValidity: 1},
	}
	ctx := context.Background()
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	sim.ExpireDelta("u@ex.com")
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	resets := sink.OfType(events.MailboxReset)
	if len(resets) == 0 {
		t.Fatalf("want mailbox.reset, events=%+v", sink.Events)
	}
	data, ok := resets[0].Data.(*types.MailboxReset)
	if !ok || data.NewUIDValidity <= data.OldUIDValidity {
		t.Fatalf("%+v", resets[0].Data)
	}
}

func TestSubscription_validationRenewClientState(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")

	acc := graph.Account{
		ID: "acc_3", OrganizationID: "org_1", Email: "u@ex.com",
		NotificationURL: "https://eb.example/callbacks/graph",
	}
	r := &graph.Runner{
		Client: &graph.Client{BaseURL: sim.URL()},
		Tokens: graph.StaticToken{Token: "tok"},
		Sink:   &graph.MemorySink{},
		State:  &graph.SyncState{UIDValidity: 1},
		Now:    func() time.Time { return time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC) },
	}
	ctx := context.Background()
	if err := r.EnsureSubscription(ctx, acc, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if r.State.SubscriptionID == "" || r.State.ClientState == "" {
		t.Fatalf("sub %+v", r.State)
	}
	// Force renewal
	past := r.Now().Add(-time.Hour)
	r.State.SubscriptionExpires = &past
	oldID := r.State.SubscriptionID
	if err := r.EnsureSubscription(ctx, acc, 24*time.Hour); err != nil {
		t.Fatal(err)
	}
	if r.State.SubscriptionExpires == nil || !r.State.SubscriptionExpires.After(r.Now()) {
		t.Fatalf("not renewed %+v", r.State.SubscriptionExpires)
	}
	_ = oldID

	h := &graph.CallbackHandler{}
	h.Register(r.State)

	// Validation handshake
	req := httptest.NewRequest(http.MethodPost, "/callbacks/graph?validationToken=vt-123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "vt-123" {
		t.Fatalf("validation %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("ct %s", ct)
	}

	// Bad clientState → 401
	bad := `{"value":[{"subscriptionId":"` + r.State.SubscriptionID + `","clientState":"wrong","changeType":"created"}]}`
	req2 := httptest.NewRequest(http.MethodPost, "/callbacks/graph", strings.NewReader(bad))
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != 401 {
		t.Fatalf("want 401 got %d", rec2.Code)
	}

	// Good notification
	good := string(sim.NotifyPayload(r.State.SubscriptionID))
	req3 := httptest.NewRequest(http.MethodPost, "/callbacks/graph", strings.NewReader(good))
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req3)
	if rec3.Code != 200 {
		t.Fatalf("notify %d %s", rec3.Code, rec3.Body.String())
	}
}

func TestClientCredentials_andSendPatch(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()

	creds := &graph.ClientCredentials{
		TokenURL: sim.URL() + "/token",
		ClientID: "cid", Secret: "sec",
	}
	tok, exp, err := creds.Token(context.Background())
	if err != nil || tok == "" || exp.IsZero() {
		t.Fatalf("%v %s %v", err, tok, exp)
	}
	src := graph.AppOnlySource{Creds: creds, UPN: "user@example.com"}
	access, user, err := src.AccessToken(context.Background(), "acc")
	if err != nil || access == "" || user != "user@example.com" {
		t.Fatalf("%v %s %s", err, access, user)
	}

	sim.SetToken("user@example.com", access)
	cli := &graph.Client{BaseURL: sim.URL(), UPN: "user@example.com"}
	raw := []byte("From: user@example.com\r\nTo: x@ex.com\r\nSubject: S\r\nMessage-ID: <s@ex>\r\n\r\nok")
	id, conv, err := cli.Send(context.Background(), access, raw)
	if err != nil || id == "" {
		t.Fatalf("send %v %s %s", err, id, conv)
	}
	inboxID, _, fid := sim.PutMessage("user@example.com", rawMsg(), "inbox")
	_ = fid
	if err := cli.Patch(context.Background(), access, inboxID, map[string]any{"isRead": true}); err != nil {
		t.Fatal(err)
	}
	m, err := cli.GetMessage(context.Background(), access, inboxID)
	if err != nil || !m.IsRead {
		t.Fatalf("%v %+v", err, m)
	}
	mime, err := cli.GetMIME(context.Background(), access, inboxID)
	if err != nil || !strings.Contains(string(mime), "Hello Graph") {
		t.Fatalf("%v %s", err, mime)
	}
}

func TestUpdateAndDelete(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")
	id, _, _ := sim.PutMessage("u@ex.com", rawMsg(), "inbox")

	acc := graph.Account{ID: "acc_4", OrganizationID: "org_1", Email: "u@ex.com", Paths: []string{"INBOX"}}
	sink := &graph.MemorySink{}
	r := &graph.Runner{
		Client: &graph.Client{BaseURL: sim.URL()},
		Tokens: graph.StaticToken{Token: "tok"},
		Sink:   sink,
		State:  &graph.SyncState{UIDValidity: 1},
	}
	ctx := context.Background()
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	// establish link then change
	sim.SetRead("u@ex.com", id, true)
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	// First sync was baseline (silent). Second sees existing indexed + flag change.
	// Actually first sync had the message already → silent index. Second: flag change → updated.
	updates := sink.OfType(events.MessageUpdated)
	if len(updates) == 0 {
		// If first sync stored with unread and second with read — should update.
		// But simulator delta returns all; first sync indexed as unread; second as read.
		t.Fatalf("want message.updated, events=%+v", sink.Events)
	}

	sim.DeleteMessage("u@ex.com", id)
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	dels := sink.OfType(events.MessageDeleted)
	if len(dels) == 0 {
		t.Fatalf("want message.deleted, events=%+v", sink.Events)
	}
}

func TestHandleValidationHelper(t *testing.T) {
	rec := httptest.NewRecorder()
	graph.HandleValidation(rec, "abc")
	if rec.Body.String() != "abc" {
		t.Fatal(rec.Body.String())
	}
}
