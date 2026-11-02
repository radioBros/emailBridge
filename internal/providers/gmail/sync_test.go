package gmail_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	gmail "github.com/radiobros/emailbridge/internal/providers/gmail"
	"github.com/radiobros/emailbridge/internal/store"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

type staticTok struct{ tok, user string }

func (s staticTok) AccessToken(context.Context, string) (string, string, error) {
	return s.tok, s.user, nil
}

func sampleRaw() []byte {
	return []byte("From: Alice <alice@ex.com>\r\nTo: Bob <bob@ex.com>\r\nSubject: Hello\r\n" +
		"Message-ID: <same@ex.com>\r\nDate: Mon, 26 Oct 2026 12:00:00 +0000\r\n\r\nHi there\r\n")
}

func TestSync_historyAndExpiry(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")

	acc := &gmail.Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "a@ex.com", Paths: []string{"*"},
	}
	sink := &gmail.MemorySink{}
	r := &gmail.Runner{
		Client: &gmail.Client{BaseURL: sim.URL()},
		Tokens: staticTok{tok: "tok", user: "a@ex.com"},
		Sink:   sink,
		State:  &gmail.MemoryState{},
		Config: gmail.Config{PullInterval: time.Hour},
		Needs:  gmail.Needs{Text: true, TextMax: 4096},
	}

	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	if len(sink.ReceivedOf()) != 0 {
		t.Fatalf("baseline must be silent, got %d", len(sink.ReceivedOf()))
	}

	sim.PutMessage("a@ex.com", sampleRaw(), []string{"INBOX", "UNREAD"})
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	got := sink.ReceivedOf()
	if len(got) != 1 {
		t.Fatalf("want 1 received, got %d", len(got))
	}
	if got[0].Subject != "Hello" || got[0].EmailID == "" || got[0].ThreadID == "" {
		t.Fatalf("%+v", got[0])
	}
	if got[0].Mailbox.Path != "INBOX" {
		t.Fatalf("mailbox %s", got[0].Mailbox.Path)
	}

	sink.Events = nil
	sim.ExpireHistory("a@ex.com")
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	var resets int
	for _, e := range sink.Events {
		if e.Type == events.MailboxReset {
			resets++
		}
		if e.Type == events.MessageReceived {
			t.Fatal("full resync must not re-emit received")
		}
	}
	if resets == 0 {
		t.Fatal("expected mailbox.reset after history expiry")
	}
}

func TestParity_imapShapedVsGmail(t *testing.T) {
	raw := sampleRaw()
	acc := &gmail.Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	internal := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

	msG := &gmail.MemoryState{}
	stG, _ := msG.Load(context.Background(), acc)
	gMsg := &gmail.Message{
		ID: "gm1", ThreadID: "th1", LabelIDs: []string{"INBOX"},
		InternalDate: internal, Size: int64(len(raw)), Raw: raw,
	}
	g, err := gmail.MapMessage(acc, stG, "INBOX", `\Inbox`, gMsg, gmail.Needs{Text: true, TextMax: 4096}, nil)
	if err != nil {
		t.Fatal(err)
	}

	msI := &gmail.MemoryState{}
	stI, _ := msI.Load(context.Background(), acc)
	iMsg := &gmail.Message{
		ID: "imap-synth", ThreadID: "imap-thread", LabelIDs: []string{"INBOX"},
		InternalDate: internal, Size: int64(len(raw)), Raw: raw,
	}
	imapLike, err := gmail.MapMessage(acc, stI, "INBOX", `\Inbox`, iMsg, gmail.Needs{Text: true, TextMax: 4096}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareExceptProvider(imapLike, g); err != nil {
		t.Fatal(err)
	}
}

func compareExceptProvider(a, b *types.MessageReceived) error {
	strip := func(d *types.MessageReceived) any {
		return map[string]any{
			"subject": d.Subject, "from": d.From, "to": d.To, "cc": d.Cc,
			"messageIdHeader": d.MessageIDHeader, "flags": d.Flags, "labels": d.Labels,
			"size": d.Size, "path": d.Mailbox.Path, "specialUse": d.Mailbox.SpecialUse,
			"text": d.Text, "date": d.Date, "internalDate": d.InternalDate.UTC(),
		}
	}
	xb, _ := json.Marshal(strip(a))
	yb, _ := json.Marshal(strip(b))
	if string(xb) != string(yb) {
		return fmt.Errorf("parity mismatch:\n%s\n%s", xb, yb)
	}
	return nil
}

func TestPush_vsPull(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")
	acc := &gmail.Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "a@ex.com", Paths: []string{"*"}, TopicName: "projects/p/topics/t",
	}
	sink := &gmail.MemorySink{}
	r := &gmail.Runner{
		Client: &gmail.Client{BaseURL: sim.URL()},
		Tokens: staticTok{tok: "tok", user: "a@ex.com"},
		Sink:   sink, State: &gmail.MemoryState{},
		Config: gmail.Config{PullInterval: time.Hour},
	}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	sim.PutMessage("a@ex.com", sampleRaw(), []string{"INBOX", "UNREAD"})

	nudged := make(chan string, 1)
	h := &gmail.PushHandler{
		Audience: "http://cb/callbacks/gmail",
		Verify:   gmail.StaticVerifier{Token: "oidc"},
		Lookup: func(_ context.Context, email string) (string, string, error) {
			if email == "a@ex.com" {
				return acc.ID, acc.OrganizationID, nil
			}
			return "", "", nil
		},
		Nudge: func(_ context.Context, accountID string) { nudged <- accountID },
	}
	data := base64.StdEncoding.EncodeToString(sim.PushPayload("a@ex.com"))
	body, _ := json.Marshal(map[string]any{
		"message": map[string]any{"data": data, "messageId": "m1"},
	})
	req := httptest.NewRequest(http.MethodPost, "/callbacks/gmail", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer oidc")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("push status %d", rr.Code)
	}
	select {
	case id := <-nudged:
		if id != acc.ID {
			t.Fatalf("nudge %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("push did not nudge")
	}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	if len(sink.ReceivedOf()) != 1 {
		t.Fatalf("pull after push wake got %d", len(sink.ReceivedOf()))
	}

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/callbacks/gmail", strings.NewReader(string(body)))
	req2.Header.Set("Authorization", "Bearer wrong")
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 got %d", rr2.Code)
	}
}

func TestSend_andModify(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")
	c := &gmail.Client{BaseURL: sim.URL()}
	sender := &gmail.Sender{Client: c, Tokens: staticTok{tok: "tok"}}
	id, thr, err := sender.Send(context.Background(), "acc", sampleRaw())
	if err != nil || id == "" || thr == "" {
		t.Fatalf("send %v %s %s", err, id, thr)
	}
	if err := c.Modify(context.Background(), "tok", id, []string{"INBOX"}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWatch(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("a@ex.com", "tok")
	c := &gmail.Client{BaseURL: sim.URL()}
	wr, err := c.Watch(context.Background(), "tok", "projects/p/topics/t")
	if err != nil || wr.HistoryID == "" || wr.Expiration.IsZero() {
		t.Fatalf("%v %+v", err, wr)
	}
}
