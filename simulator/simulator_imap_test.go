package simulator_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

func dialIMAP(t *testing.T, env *simtest.Env, user, pass string, tlsMode imapc.TLSMode) *imapc.Client {
	t.Helper()
	addr := env.Addr.IMAPPlain
	if tlsMode == imapc.TLSImplicit {
		addr = env.Addr.IMAPTLS
	}
	cl, err := imapc.Dial(context.Background(), imapc.Options{
		Addr: addr, TLS: tlsMode, InsecureSkipVerify: true,
		Username: user, Password: pass,
		CommandTimeout: 10 * time.Second, DoneTimeout: 2 * time.Second,
		ID: map[string]string{"name": "eb-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl
}

func TestIMAPBasics(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	if err := env.Client.CreateUser(ctx, "alice", "secret"); err != nil {
		t.Fatal(err)
	}
	cl := dialIMAP(t, env, "alice", "secret", imapc.TLSNone)
	if !cl.Has("MOVE") || !cl.Has("UIDPLUS") || !cl.Has("IDLE") {
		t.Fatalf("caps: %+v", cl.Caps())
	}
	list, err := cl.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, m := range list {
		names[m.Name] = true
	}
	if !names["INBOX"] || !names["Sent"] {
		t.Fatalf("mailboxes: %v", names)
	}
	raw := []byte("From: a@b\r\nTo: c@d\r\nSubject: hi\r\nMessage-ID: <m1@t>\r\n\r\nhello\r\n")
	inj, err := env.Client.InjectMessage(ctx, "alice", "INBOX", raw, nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	sel, err := cl.Select(ctx, "INBOX", false)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Exists < 1 || sel.UIDValidity == 0 {
		t.Fatalf("select: %+v", sel)
	}
	msgs, err := cl.Fetch(ctx, 1, 0, imapc.FetchOptions{Flags: true, Envelope: true, Body: true, Size: true, InternalDate: true, HeaderFields: []string{"Message-ID"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) < 1 || msgs[0].UID != inj.UID {
		t.Fatalf("fetch: %+v want uid %d", msgs, inj.UID)
	}
	if msgs[0].Envelope == nil || msgs[0].Envelope.Subject != "hi" {
		t.Fatalf("envelope: %+v", msgs[0].Envelope)
	}
	found, err := cl.SearchMessageID(ctx, "<m1@t>")
	if err != nil || len(found) != 1 || found[0] != inj.UID {
		t.Fatalf("search: %v %v", found, err)
	}
	if err := cl.StoreFlags(ctx, []uint32{inj.UID}, true, []string{`\Seen`, `\Flagged`}); err != nil {
		t.Fatal(err)
	}
	uid, err := cl.Append(ctx, "Sent", []string{`\Seen`}, time.Now(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if uid == 0 {
		t.Fatal("APPENDUID expected")
	}
	if err := cl.Move(ctx, []uint32{inj.UID}, "Trash"); err != nil {
		t.Fatal(err)
	}
	if _, err := cl.Select(ctx, "Trash", false); err != nil {
		t.Fatal(err)
	}
	_ = cl.StoreFlags(ctx, []uint32{inj.UID}, true, []string{`\Deleted`})
	// UID may differ after move — search again
	uids, _ := cl.UIDs(ctx)
	if len(uids) > 0 {
		_ = cl.StoreFlags(ctx, uids, true, []string{`\Deleted`})
		if err := cl.Expunge(ctx, uids); err != nil {
			t.Fatal(err)
		}
	}
	if err := cl.Noop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cl.Logout(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIMAPImplicitTLS(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "bob", "x")
	cl := dialIMAP(t, env, "bob", "x", imapc.TLSImplicit)
	if _, err := cl.List(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestIDLEAfterInject(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "idle", "x")
	cl := dialIMAP(t, env, "idle", "x", imapc.TLSNone)
	if _, err := cl.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- cl.Idle(ctx, 3*time.Second)
	}()
	time.Sleep(200 * time.Millisecond)
	raw := []byte("From: a@b\r\nSubject: idle\r\nMessage-ID: <idle@t>\r\n\r\nx\r\n")
	if _, err := env.Client.InjectMessage(ctx, "idle", "INBOX", raw, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	got := false
	for !got {
		select {
		case u := <-cl.Updates():
			if u.Kind == imapc.UpdateExists {
				got = true
			}
		case <-deadline:
			t.Fatal("no EXISTS update")
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("idle hung")
	}
}

func TestIMAPFaults(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "fault", "x")

	t.Run("authFail", func(t *testing.T) {
		_ = env.Client.SetIMAPFault(ctx, "fault", simulator.IMAPFault{AuthFail: true})
		_, err := imapc.Dial(ctx, imapc.Options{
			Addr: env.Addr.IMAPPlain, TLS: imapc.TLSNone, Username: "fault", Password: "x",
			CommandTimeout: 5 * time.Second,
		})
		if errs.CodeOf(err) != errs.CodeMailAuthFailed {
			t.Fatalf("got %v (%s)", err, errs.CodeOf(err))
		}
		_ = env.Client.ClearIMAPFault(ctx, "fault")
	})

	t.Run("loginLimit", func(t *testing.T) {
		_ = env.Client.SetIMAPFault(ctx, "fault", simulator.IMAPFault{LoginLimit: true})
		_, err := imapc.Dial(ctx, imapc.Options{
			Addr: env.Addr.IMAPPlain, TLS: imapc.TLSNone, Username: "fault", Password: "x",
			CommandTimeout: 5 * time.Second,
		})
		if errs.CodeOf(err) != errs.CodeMailConnectFailed || errs.KindOf(err) != errs.KindRateLimited {
			t.Fatalf("LIMIT: %v kind=%v code=%s", err, errs.KindOf(err), errs.CodeOf(err))
		}
		_ = env.Client.ClearIMAPFault(ctx, "fault")
	})

	t.Run("neverDone", func(t *testing.T) {
		_ = env.Client.ClearIMAPFault(ctx, "fault")
		cl := dialIMAP(t, env, "fault", "x", imapc.TLSNone)
		if _, err := cl.Select(ctx, "INBOX", false); err != nil {
			t.Fatal(err)
		}
		_ = env.Client.SetIMAPFault(ctx, "fault", simulator.IMAPFault{NeverDone: true})
		err := cl.Idle(ctx, 100*time.Millisecond)
		if errs.CodeOf(err) != errs.CodeMailTimeout {
			t.Fatalf("neverDone: %v (%s)", err, errs.CodeOf(err))
		}
	})

	t.Run("recreateUIDValidity", func(t *testing.T) {
		_ = env.Client.ClearIMAPFault(ctx, "fault")
		cl := dialIMAP(t, env, "fault", "x", imapc.TLSNone)
		sel1, err := cl.Select(ctx, "INBOX", false)
		if err != nil {
			t.Fatal(err)
		}
		uidv, err := env.Client.RecreateMailbox(ctx, "fault", "INBOX")
		if err != nil {
			t.Fatal(err)
		}
		if uidv == sel1.UIDValidity {
			t.Fatalf("UIDVALIDITY did not change: %d", uidv)
		}
	})
}

func TestControlAPI(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	if err := env.Client.CreateUser(ctx, "ctrl", "p"); err != nil {
		t.Fatal(err)
	}
	raw := []byte("From: a@b\r\nSubject: c\r\n\r\nbody\r\n")
	inj, err := env.Client.InjectMessage(ctx, "ctrl", "INBOX", raw, []string{`\Seen`}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Client.SetFlags(ctx, "ctrl", "INBOX", []uint32{inj.UID}, []string{`\Deleted`}, "add"); err != nil {
		t.Fatal(err)
	}
	if err := env.Client.Expunge(ctx, "ctrl", "INBOX", []uint32{inj.UID}); err != nil {
		t.Fatal(err)
	}
	caps := simulator.DefaultCaps()
	caps.MOVE = false
	if err := env.Client.SetCaps(ctx, "ctrl", caps); err != nil {
		t.Fatal(err)
	}
	got, err := env.Client.GetCaps(ctx, "ctrl")
	if err != nil || got.MOVE {
		t.Fatalf("caps: %+v %v", got, err)
	}
	if err := env.Client.CreateMailbox(ctx, "ctrl", "Custom", `\Flagged`); err != nil {
		t.Fatal(err)
	}
	if err := env.Client.RenameMailbox(ctx, "ctrl", "Custom", "Custom2"); err != nil {
		t.Fatal(err)
	}
	if err := env.Client.DeleteMailbox(ctx, "ctrl", "Custom2"); err != nil {
		t.Fatal(err)
	}
	addrs, err := env.Client.Addrs(ctx)
	if err != nil || addrs.Control == "" {
		t.Fatalf("addrs: %+v %v", addrs, err)
	}
	_ = env.Client.ClearSMTPMessages(ctx)
	if err := env.Client.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	_ = env.Client.DeleteUser(ctx, "ctrl") // 404 after reset is fine
	_ = strings.TrimSpace("")
}

func TestFleet(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	if err := env.Client.StartFleet(ctx, simulator.FleetOptions{Users: 3, RatePerSec: 50, Prefix: "f"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var exists uint32
	for time.Now().Before(deadline) {
		u := env.Sim.Store().User("f00000")
		if u != nil {
			st, err := u.Status("INBOX", &imap.StatusOptions{NumMessages: true})
			if err == nil && st.NumMessages != nil && *st.NumMessages > 0 {
				exists = *st.NumMessages
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = env.Client.StopFleet(ctx)
	if exists == 0 {
		t.Fatal("expected fleet messages")
	}
}

func TestSessionStats(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	if err := env.Client.CreateUser(ctx, "sess", "p"); err != nil {
		t.Fatal(err)
	}
	a := dialIMAP(t, env, "sess", "p", imapc.TLSNone)
	_ = dialIMAP(t, env, "sess", "p", imapc.TLSNone)
	st, err := env.Client.SessionStats(ctx, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if st.Logins != 2 || st.Open != 2 || st.PeakOpen != 2 {
		t.Fatalf("two sessions: %+v", st)
	}
	_ = a.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ = env.Client.SessionStats(ctx, "sess")
		if st.Open == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if st.Logins != 2 || st.Open != 1 || st.PeakOpen != 2 {
		t.Fatalf("after one close: %+v", st)
	}
	if none, err := env.Client.SessionStats(ctx, "nobody"); err != nil || none.Logins != 0 {
		t.Fatalf("unknown user: %+v %v", none, err)
	}
}
