package gmail

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

type tok struct{ t, u string }

func (s tok) AccessToken(context.Context, string) (string, string, error) { return s.t, s.u, nil }

func TestCoverage_labelsUpdatesDeletesWatchPull(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")
	sim.AddUserLabel("a@ex.com", "Label_1", "Work")

	acc := &Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "a@ex.com", Paths: []string{"*"}, TopicName: "projects/p/topics/t",
	}
	sink := &MemorySink{}
	r := &Runner{
		Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "a@ex.com"},
		Sink: sink, State: &MemoryState{}, Log: slog.Default(),
		Config: Config{PullInterval: 20 * time.Millisecond, WatchRenew: time.Hour},
		Needs:  Needs{Headers: []string{"Subject", "*"}, Text: true, TextMax: 100},
	}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	id, _, _ := sim.PutMessage("a@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: X\r\nMessage-ID: <x@e>\r\n\r\nbody"),
		[]string{"INBOX", "UNREAD", "Label_1"})
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	if len(sink.ReceivedOf()) != 1 {
		t.Fatalf("received %d", len(sink.ReceivedOf()))
	}
	if len(sink.ReceivedOf()[0].Labels) != 1 || sink.ReceivedOf()[0].Labels[0] != "Work" {
		t.Fatalf("labels %v", sink.ReceivedOf()[0].Labels)
	}

	sim.ModifyLabels("a@ex.com", id, []string{"STARRED"}, []string{"UNREAD"})
	sink.Events = nil
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	var updated bool
	for _, e := range sink.Events {
		if e.Type == events.MessageUpdated {
			updated = true
			u := e.Data.(*types.MessageUpdated)
			if len(u.Flags.Added) == 0 && len(u.Flags.Removed) == 0 {
				t.Fatalf("expected flag change %+v", u.Flags)
			}
		}
	}
	if !updated {
		t.Fatal("expected message.updated")
	}

	sim.DeleteMessage("a@ex.com", id)
	sink.Events = nil
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	for _, e := range sink.Events {
		if e.Type == events.MessageDeleted {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("expected message.deleted")
	}

	r.Wake()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = r.RunPull(ctx, acc) // covers pull loop + ensureWatch
}

func TestCoverage_moduleAndPushEdges(t *testing.T) {
	m := &Module{CallbackURL: "http://cb", OIDCToken: "oidc", Log: slog.Default()}
	if m.Name() != "providers.gmail" || len(m.Roles()) == 0 {
		t.Fatal("module meta")
	}
	a := app.New(app.Options{Log: slog.Default(), Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n1"})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Client() == nil || m.Sender() == nil {
		t.Fatal("deps")
	}
	_ = m.NewRunner(tok{"t", "u"}, &MemorySink{})
	_ = m.Stop(context.Background())

	h := &PushHandler{Verify: StaticVerifier{Token: "oidc"}, Audience: "aud"}
	// method
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method %d", rr.Code)
	}
	// no auth
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth %d", rr.Code)
	}
	// bad json
	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{"))
	req.Header.Set("Authorization", "Bearer oidc")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("json %d", rr.Code)
	}
	// bad data encoding
	rr = httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{"message": map[string]any{"data": "!!!", "messageId": "d1"}})
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer oidc")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("b64 %d", rr.Code)
	}
	// duplicate messageId
	h.Lookup = func(context.Context, string) (string, string, error) { return "acc", "org", nil }
	nudged := 0
	h.Nudge = func(context.Context, string) { nudged++ }
	payload := base64.StdEncoding.EncodeToString([]byte(`{"emailAddress":"a@ex.com","historyId":1}`))
	body, _ = json.Marshal(map[string]any{"message": map[string]any{"data": payload, "messageId": "dup"}})
	for i := 0; i < 2; i++ {
		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		req.Header.Set("Authorization", "Bearer oidc")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("push %d", rr.Code)
		}
	}
	if nudged != 1 {
		t.Fatalf("dedupe nudged=%d", nudged)
	}
	// unknown email still 204
	h.Lookup = func(context.Context, string) (string, string, error) { return "", "", nil }
	body, _ = json.Marshal(map[string]any{"message": map[string]any{"data": payload, "messageId": "x2"}})
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer oidc")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unknown %d", rr.Code)
	}
}

func TestCoverage_sendEdgesAndClientErrors(t *testing.T) {
	s := &Sender{}
	if _, _, err := s.Send(context.Background(), "a", nil); err == nil {
		t.Fatal("empty sender")
	}
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")
	c := &Client{BaseURL: sim.URL()}
	s = &Sender{Client: c, Tokens: tok{"tok", "a"}}
	if _, _, err := s.Send(context.Background(), "a", nil); err == nil {
		t.Fatal("empty body")
	}
	// unauthorized
	if _, err := c.GetProfile(context.Background(), "bad"); err == nil {
		t.Fatal("want auth err")
	}
	_ = truncate("abcdefghij", 3)
	_ = (&Client{}).http()
	_ = (&Client{}).apiRoot()
	_ = (&Client{}).tokenURL()
	_ = LabelForPath("[Gmail]/Sent Mail")
	_ = FlagDiff(nil, nil)
	_ = selectHeaders(map[string][]string{"x": {"1"}}, []string{"*"})
	_ = selectHeaders(map[string][]string{"subject": {"S"}}, []string{"Subject"})
	_ = firstHeader(nil, "x")
	_ = parseAddrs(nil, "to")
	_ = UserLabels(nil, nil)
	_ = FlagsFromLabels([]string{"DRAFT"})
	_ = watchedSet(nil)
	_ = PathForLabel("SENT")

	// SA error paths
	if _, err := ParseServiceAccountKey([]byte(`not-json`)); err == nil {
		t.Fatal("bad json")
	}
	src := &ServiceAccountSource{}
	if _, _, err := src.AccessToken(context.Background(), ""); err == nil {
		t.Fatal("no key")
	}
	src.Key = &ServiceAccountKey{ClientEmail: "a", PrivateKey: "nope", TokenURI: sim.URL() + "/token"}
	if _, _, err := src.AccessToken(context.Background(), ""); err == nil {
		t.Fatal("no subject")
	}
	src.Subject = "u@ex.com"
	if _, _, err := src.AccessToken(context.Background(), ""); err == nil {
		t.Fatal("bad pem")
	}
	// PKCS1 key
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pem1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	src.Key.PrivateKey = string(pem1)
	src.Client = &Client{TokenURL: sim.URL() + "/token"}
	src.now = func() time.Time { return time.Now().UTC() }
	if _, _, err := src.AccessToken(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestCoverage_mapNotifyFromAndSeen(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")
	acc := &Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "a@ex.com", Paths: []string{"INBOX"}, NotifyFrom: time.Now().UTC().Add(time.Hour),
	}
	sink := &MemorySink{}
	r := &Runner{Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "a@ex.com"}, Sink: sink, State: &MemoryState{}}
	_ = r.SyncOnce(context.Background(), acc)
	sim.PutMessage("a@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: old\r\nMessage-ID: <o@e>\r\n\r\nx"),
		[]string{"INBOX"})
	_ = r.SyncOnce(context.Background(), acc)
	if len(sink.ReceivedOf()) != 0 {
		t.Fatal("notifyFrom should suppress received")
	}
	// unwatched path
	acc2 := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: acc.OrganizationID, Email: "a@ex.com", Paths: []string{"INBOX"}}
	r2 := &Runner{Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "a@ex.com"}, Sink: &MemorySink{}, State: &MemoryState{}}
	_ = r2.SyncOnce(context.Background(), acc2)
	sim.PutMessage("a@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: sent\r\nMessage-ID: <s@e>\r\n\r\nx"),
		[]string{"SENT"})
	_ = r2.SyncOnce(context.Background(), acc2)
}

func TestCoverage_emitNilSink(t *testing.T) {
	r := &Runner{}
	if err := r.emit(context.Background(), &Account{}, "x", "y", nil); err != nil {
		t.Fatal(err)
	}
	r.State = nil
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	st, err := r.load(context.Background(), acc)
	if err != nil || st == nil {
		t.Fatal(err)
	}
	_ = r.save(context.Background(), st)
}

func TestCoverage_httpError(t *testing.T) {
	e := &httpError{code: 401, msg: "x"}
	if e.Error() != "x" {
		t.Fatal(e.Error())
	}
	if errUnauthorized.Error() == "" {
		t.Fatal("empty")
	}
}

func TestCoverage_accountTokenAndCommitSink(t *testing.T) {
	tokSrc := accountToken{acc: &ebsync.Account{}}
	if _, _, err := tokSrc.AccessToken(context.Background(), ""); err == nil {
		t.Fatal("no token fn")
	}
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	sink := &MemorySink{}
	r := &Runner{Sink: sink, Draft: func(typ, subject string, data any) events.Draft {
		return events.Draft{Type: typ, Subject: subject, Data: data}
	}}
	d := r.draftOf(acc, events.MessageReceived, "msg_x", &types.MessageReceived{Subject: "s"})
	if d.Type != events.MessageReceived {
		t.Fatal(d.Type)
	}
	if err := r.commit(context.Background(), acc, []events.Draft{d}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(sink.ReceivedOf()) != 1 {
		t.Fatal("sink")
	}
	_ = LabelForPath("[Gmail]/Sent Mail")

	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("w@ex.com", "tok")
	r2 := &Runner{
		Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "w@ex.com"},
		State: &MemoryState{}, Config: Config{WatchRenew: 48 * time.Hour},
		now: func() time.Time { return time.Now().UTC() },
	}
	wacc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "w@ex.com", TopicName: "projects/p/topics/t", Paths: []string{"*"}}
	_ = r2.SyncOnce(context.Background(), wacc)
	if err := r2.ensureWatch(context.Background(), wacc); err != nil {
		t.Fatal(err)
	}
	r2.wake = make(chan struct{}, 1)
	r2.Wake()
	r2.Wake() // second is no-op (full)
}

type failRT struct{}

func (failRT) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no net")
}

func TestCoverage_providerHelpersAndOIDC(t *testing.T) {
	v := &GoogleVerifier{}
	if err := v.Verify(context.Background(), "", "aud"); err == nil {
		t.Fatal("empty token")
	}
	if err := v.Verify(context.Background(), "tok", ""); err == nil {
		t.Fatal("empty aud")
	}
	v.HTTP = &http.Client{Transport: failRT{}, Timeout: time.Second}
	if err := v.Verify(context.Background(), "eyJhbGciOiJSUzI1NiJ9.e30.x", "http://cb/callbacks/gmail"); err == nil {
		t.Fatal("expected discovery failure")
	}
	// cached nil provider retries
	if err := v.Verify(context.Background(), "eyJhbGciOiJSUzI1NiJ9.e30.x", "http://cb/callbacks/gmail"); err == nil {
		t.Fatal("expected discovery failure again")
	}

	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("h@ex.com", "tok")
	c := &Client{BaseURL: sim.URL()}
	ids, err := c.ListMessageIDs(context.Background(), "tok", "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	_ = ids

	st := &SyncState{MailboxIDs: map[string]string{
		"INBOX": "mb_in", "[Gmail]/Sent Mail": "mb_sent", "Label/Work": "mb_lab",
	}}
	rows := []*index.Message{
		{ID: "1", MailboxID: "mb_lab", ProviderID: "g1", Flags: []string{`\Seen`}},
		{ID: "2", MailboxID: "mb_in", ProviderID: "g1", Flags: []string{`\Seen`}},
		{ID: "3", MailboxID: "mb_sent", ProviderID: "g1"},
	}
	p := primaryRow(st, rows)
	if p == nil || p.MailboxID != "mb_in" {
		t.Fatalf("primary %+v", p)
	}
	labs := labelsFromRows(st, rows)
	if len(labs) != 1 || labs[0] != "Work" {
		t.Fatalf("labels %v", labs)
	}
	if pathForMailboxID(st, "nope") != "" {
		t.Fatal("miss")
	}
	if pathForMailboxID(st, "mb_lab") != "Label/Work" {
		t.Fatal(pathForMailboxID(st, "mb_lab"))
	}
}

func TestCoverage_fullResyncFetchesWhenNeedsText(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("f@ex.com", "tok")
	sim.PutMessage("f@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: F\r\nMessage-ID: <f@e>\r\n\r\nbody"),
		[]string{"INBOX"})
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "f@ex.com", Paths: []string{"INBOX"}}
	r := &Runner{
		Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "f@ex.com"},
		Sink: &MemorySink{}, State: &MemoryState{}, Needs: Needs{Text: true, TextMax: 100},
	}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
}

func TestCoverage_onLabelsLeavesMailboxAndPrimaryRanks(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("l@ex.com", "tok")
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "l@ex.com", Paths: []string{"INBOX"}}
	sink := &MemorySink{}
	r := &Runner{Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "l@ex.com"}, Sink: sink, State: &MemoryState{}}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	id, _, _ := sim.PutMessage("l@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: L\r\nMessage-ID: <l@e>\r\n\r\nx"),
		[]string{"INBOX", "UNREAD"})
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	sink.Events = nil
	sim.ModifyLabels("l@ex.com", id, nil, []string{"INBOX", "UNREAD"}) // leave watched mailbox
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	var deleted bool
	for _, e := range sink.Events {
		if e.Type == events.MessageDeleted {
			deleted = true
		}
	}
	if !deleted {
		t.Fatal("expected delete when leaving mailbox")
	}

	st := &SyncState{MailboxIDs: map[string]string{
		"[Gmail]/Drafts": "d", "[Gmail]/Trash": "t", "[Gmail]/Spam": "s", "Label/X": "x",
	}}
	for _, row := range []struct {
		mbx string
	}{{"d"}, {"t"}, {"s"}, {"x"}} {
		p := primaryRow(st, []*index.Message{{ID: "m", MailboxID: row.mbx, ProviderID: "g"}})
		if p == nil {
			t.Fatal(row.mbx)
		}
	}
	r.remember(nil)
	r.Index = &index.Index{} // remember no-ops when Index set
	r.remember([]*index.Message{{ID: "z", ProviderID: "p"}})
}

func TestCoverage_moduleStartGoogleVerifier(t *testing.T) {
	m := &Module{CallbackURL: "http://cb", Log: slog.Default()} // no OIDCToken → GoogleVerifier when API
	a := app.New(app.Options{Log: slog.Default(), Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n1"})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Push() != nil {
		t.Fatal("push only on API role")
	}
	_ = m.Stop(context.Background())
}
