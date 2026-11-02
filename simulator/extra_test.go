package simulator

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/simulator/mem"
)

func TestMemSearchAndFlags(t *testing.T) {
	u := mem.NewUser("u", "p")
	u.EnsureDefaults()
	raw := []byte("From: a@b\r\nTo: c@d\r\nSubject: findme\r\nMessage-ID: <x@y>\r\nDate: Mon, 02 Jan 2006 15:04:05 -0700\r\n\r\nhello world body\r\n")
	uid, _, err := u.InjectMessage("INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mbox, _ := u.Mailbox("INBOX")
	mbox.SetSpecialUse([]imap.MailboxAttr{imap.MailboxAttrAll})
	_ = mbox.Name()
	_ = u.Password()
	_ = u.Subscribe("INBOX")
	_ = u.Unsubscribe("INBOX")
	_ = u.Subscribe("INBOX")
	ns, err := u.Namespace()
	if err != nil || len(ns.Personal) == 0 {
		t.Fatal(ns, err)
	}
	st, err := u.Status("INBOX", &imap.StatusOptions{NumMessages: true, UIDNext: true, UIDValidity: true, NumUnseen: true, NumDeleted: true, Size: true, NumRecent: true})
	if err != nil || st.NumMessages == nil || *st.NumMessages != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	mbox.SetFlags([]imap.UID{imap.UID(uid)}, []imap.Flag{imap.FlagDeleted}, imap.StoreFlagsSet)
	mbox.SetFlags([]imap.UID{imap.UID(uid)}, []imap.Flag{imap.FlagSeen}, imap.StoreFlagsAdd)
	mbox.SetFlags([]imap.UID{imap.UID(uid)}, []imap.Flag{imap.FlagSeen}, imap.StoreFlagsDel)
	view := mbox.NewView()
	defer view.Close()
	data, err := view.Search(imapserver.NumKindUID, &imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{{Key: "Subject", Value: "findme"}},
		Text:   []string{"hello"},
		Body:   []string{"world"},
	}, &imap.SearchOptions{})
	if err != nil || data.Count != 1 {
		t.Fatalf("search: %+v %v", data, err)
	}
	srv := mem.New()
	srv.AddUser(u)
	if len(srv.Users()) != 1 {
		t.Fatal(srv.Users())
	}
	mbox.Reset(99)
	if mbox.UIDValidity() != 99 {
		t.Fatal(mbox.UIDValidity())
	}
}

func TestLoginServer(t *testing.T) {
	ok := false
	s := &loginServer{auth: func(_, user, pass string) error {
		if user == "u" && pass == "p" {
			ok = true
			return nil
		}
		return sasl.ErrUnexpectedClientResponse
	}}
	ch, done, err := s.Next(nil)
	if err != nil || done || string(ch) != "Username:" {
		t.Fatalf("%q %v %v", ch, done, err)
	}
	ch, done, err = s.Next([]byte("u"))
	if err != nil || done || string(ch) != "Password:" {
		t.Fatalf("%q %v %v", ch, done, err)
	}
	_, done, err = s.Next([]byte("p"))
	if err != nil || !done || !ok {
		t.Fatalf("done=%v err=%v ok=%v", done, err, ok)
	}
	s2 := &loginServer{auth: func(_, user, pass string) error {
		if user == "a" && pass == "b" {
			return nil
		}
		return sasl.ErrUnexpectedClientResponse
	}}
	_, _, _ = s2.Next([]byte("a")) // IR username
	_, done, err = s2.Next([]byte("b"))
	if err != nil || !done {
		t.Fatal(err, done)
	}
}

func TestIMAPMoreCommandsAndFaults(t *testing.T) {
	sim, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	_, _ = sim.CreateUser("more", "x")
	_ = sim.AddressesJSON()
	_ = sim.TLSConfig()
	_ = sim.Store()

	ctx := context.Background()
	cl, err := imapc.Dial(ctx, imapc.Options{
		Addr: sim.Addr().IMAPPlain, TLS: imapc.TLSNone, Username: "more", Password: "x",
		CommandTimeout: 10 * time.Second, DoneTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()

	st := sim.stateOf("more")
	st.setIMAPFault(&IMAPFault{Latency: 5 * time.Millisecond, Remaining: 2})
	if _, err := cl.Status(ctx, "INBOX"); err != nil {
		t.Fatal(err)
	}
	st.setIMAPFault(&IMAPFault{SlowFetch: 5 * time.Millisecond})
	raw := []byte("From: a@b\r\nSubject: c\r\nMessage-ID: <c@d>\r\n\r\nz\r\n")
	_, _ = cl.Append(ctx, "INBOX", nil, time.Now(), raw)
	if _, err := cl.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Fetch(ctx, 1, 0, imapc.FetchOptions{Flags: true}); err != nil {
		t.Fatal(err)
	}
	st.clearIMAPFault()
	st.setIMAPFault(&IMAPFault{Bye: true})
	_ = cl.Noop(ctx) // may fail with bye/drop
	st.clearIMAPFault()

	cl2, err := imapc.Dial(ctx, imapc.Options{
		Addr: sim.Addr().IMAPPlain, TLS: imapc.TLSNone, Username: "more", Password: "x",
		CommandTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl2.Close() }()
	caps := DefaultCaps()
	caps.MOVE = false
	caps.SpecialUse = false
	caps.IDLE = false
	st.setCaps(caps)
	if _, err := cl2.List(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := cl2.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	uids, _ := cl2.UIDs(ctx)
	if len(uids) > 0 {
		// MOVE disabled → imapc falls back to COPY+delete
		_ = cl2.Move(ctx, uids[:1], "Trash")
	}
	st.setIMAPFault(&IMAPFault{DropAfterCmds: 1})
	_ = cl2.Noop(ctx)

	cl3, err := imapc.Dial(ctx, imapc.Options{
		Addr: sim.Addr().IMAPPlain, TLS: imapc.TLSNone, Username: "more", Password: "x",
		CommandTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl3.Close() }()
	st.clearIMAPFault()
	st.setCaps(DefaultCaps())
	if _, err := cl3.Select(ctx, "INBOX", true); err != nil { // examine
		t.Fatal(err)
	}
}

func TestSMTPLoginAndClear(t *testing.T) {
	sim, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	_, _ = sim.CreateUser("loginuser", "pw")
	st := sim.stateOf("loginuser")
	st.setSMTP(nil) // nil → empty
	st.setSMTP(SMTPScript{"mailFrom": {Code: 550, Text: "no"}})
	s, err := smtpc.Open(context.Background(), smtpc.SessionOptions{
		Addr: sim.Addr().SMTPPlain, TLS: smtpc.TLSNone, Username: "loginuser", Password: "pw",
		ConnectTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second, DataTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Send(context.Background(), smtpc.Envelope{From: "a@t", To: []string{"b@t"}}, strings.NewReader("x"), smtpc.Hooks{})
	if err == nil {
		t.Fatal("expected mailFrom fault")
	}
	_ = s.Close()
	st.clearSMTP()
	sim.ClearSMTP()
	if len(sim.ListSMTP()) != 0 {
		t.Fatal("cleared")
	}
}

func TestControlErrorPaths(t *testing.T) {
	sim, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	base := "http://" + sim.Addr().Control
	post := func(path, body string) int {
		res, err := http.Post(base+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		return res.StatusCode
	}
	if post("/users", `{}`) != http.StatusBadRequest {
		t.Fatal("create user")
	}
	if post("/users", `{"username":"dup","password":"p"}`) != http.StatusCreated {
		t.Fatal("create")
	}
	if post("/users", `{"username":"dup","password":"p"}`) != http.StatusConflict {
		t.Fatal("dup")
	}
	if post("/users/dup/mailboxes", `{}`) != http.StatusBadRequest {
		t.Fatal("mbox")
	}
	if post("/users/dup/mailbox/inject", `{}`) != http.StatusBadRequest {
		t.Fatal("inject")
	}
	if post("/users/dup/mailbox/rename", `{}`) != http.StatusBadRequest {
		t.Fatal("rename")
	}
	if post("/fleet", `{"users":0}`) != http.StatusBadRequest {
		t.Fatal("fleet")
	}
	res, _ := http.Get(base + "/health")
	_ = res.Body.Close()
}

func TestParseEnhancedAndSMTPErr(t *testing.T) {
	if parseEnhanced("bad") != smtp.NoEnhancedCode {
		t.Fatal(parseEnhanced("bad"))
	}
	ec := parseEnhanced("5.7.1")
	if ec[0] != 5 || ec[1] != 7 || ec[2] != 1 {
		t.Fatal(ec)
	}
	if smtpErr(SMTPStageFault{}) != nil {
		t.Fatal("empty")
	}
	if smtpErr(SMTPStageFault{Code: 550, Text: ""}) == nil {
		t.Fatal("expected err")
	}
	dropConn(nil)
}

func TestFleetDirect(t *testing.T) {
	sim, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := sim.StartFleet(ctx, FleetOptions{Users: 2, RatePerSec: 50, Prefix: "d"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	sim.StopFleet()
	if sim.store.User("d00000") == nil {
		t.Fatal("missing fleet user")
	}
	if err := sim.StartFleet(ctx, FleetOptions{}); err == nil {
		t.Fatal("expected users error")
	}
}

func TestFaultSessionMailboxOps(t *testing.T) {
	store := mem.New()
	u := mem.NewUser("fs", "p")
	u.EnsureDefaults()
	store.AddUser(u)
	sim := &Simulator{store: store, states: map[string]*userState{"fs": newUserState()}}
	inner := store.NewSession()
	if err := inner.Login("fs", "p"); err != nil {
		t.Fatal(err)
	}
	s := &faultSession{inner: inner, sim: sim, user: "fs", state: sim.stateOf("fs"), authed: true}
	if err := s.Create("NewBox", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Subscribe("NewBox"); err != nil {
		t.Fatal(err)
	}
	if err := s.Unsubscribe("NewBox"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename("NewBox", "Renamed", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Namespace(); err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@b\r\nSubject: x\r\n\r\ny\r\n")
	_, _, _ = u.InjectMessage("INBOX", raw, nil, time.Now())
	if _, err := s.Select("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	var set imap.UIDSet
	set.AddNum(1)
	if _, err := s.Copy(set, "Sent"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("Renamed"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
}

func TestSectionFetch(t *testing.T) {
	sim, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sim.Stop() })
	_, _ = sim.CreateUser("sec", "p")
	u := sim.store.User("sec")
	raw := bytes.NewBuffer(nil)
	raw.WriteString("From: a@b\r\nSubject: s\r\nContent-Type: text/plain\r\n\r\npartbody\r\n")
	uid, _, _ := u.InjectMessage("INBOX", raw.Bytes(), nil, time.Now())
	ctx := context.Background()
	cl, err := imapc.Dial(ctx, imapc.Options{
		Addr: sim.Addr().IMAPPlain, TLS: imapc.TLSNone, Username: "sec", Password: "p",
		CommandTimeout: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cl.Close() }()
	if _, err := cl.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	b, err := cl.Raw(ctx, uid, 100)
	if err != nil || !bytes.Contains(b, []byte("partbody")) {
		t.Fatalf("%q %v", b, err)
	}
	b, err = cl.Section(ctx, uid, nil, 10)
	if err != nil || len(b) == 0 {
		t.Fatal(err, len(b))
	}
}
