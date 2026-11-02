package imapc

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// qresyncServer scripts a QRESYNC session: ENABLE, SELECT … (QRESYNC …) with VANISHED (EARLIER) and a changed
// message, the UID map fetch, and a VANISHED (no EARLIER) on NOOP.
type qresyncServer struct {
	ln  net.Listener
	mu  sync.Mutex
	got []string
}

func newQResyncServer(t *testing.T) *qresyncServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &qresyncServer{ln: ln}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })
	return s
}

func (s *qresyncServer) lines() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func (s *qresyncServer) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	const caps = "IMAP4rev1 IDLE ID ENABLE CONDSTORE QRESYNC"
	w := func(l string) { _, _ = io.WriteString(c, l+"\r\n") }
	w("* OK [CAPABILITY " + caps + "] ready")
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		s.mu.Lock()
		s.got = append(s.got, line)
		s.mu.Unlock()
		tag, rest, _ := strings.Cut(line, " ")
		cmd := strings.ToUpper(strings.Fields(rest + " x")[0])
		switch {
		case cmd == "CAPABILITY":
			w("* CAPABILITY " + caps)
			w(tag + " OK done")
		case cmd == "LOGIN":
			w(tag + " OK [CAPABILITY " + caps + "] logged in")
		case cmd == "ID":
			w(tag + " OK id")
		case cmd == "ENABLE":
			w("* ENABLED QRESYNC")
			w(tag + " OK enabled")
		case cmd == "EXAMINE" || cmd == "SELECT":
			w("* 5 EXISTS")
			w("* OK [UIDVALIDITY 7] ok")
			w("* OK [UIDNEXT 10] ok")
			w("* OK [HIGHESTMODSEQ 90] ok")
			if strings.Contains(rest, "QRESYNC") {
				w("* VANISHED (EARLIER) 2:3,100:4000000000")
				w(`* 2 FETCH (UID 4 FLAGS (\Seen) MODSEQ (80))`)
			}
			w(tag + " OK [READ-ONLY] selected")
		case cmd == "FETCH" || strings.HasPrefix(strings.ToUpper(rest), "UID FETCH"):
			for i, uid := range []string{"1", "4", "5", "6", "7"} {
				w("* " + string(rune('1'+i)) + " FETCH (UID " + uid + ")")
			}
			w(tag + " OK fetched")
		case cmd == "NOOP":
			w("* VANISHED 5:6")
			w(tag + " OK noop")
		case cmd == "LOGOUT":
			w("* BYE")
			w(tag + " OK bye")
			return
		default:
			w(tag + " BAD unknown")
		}
	}
}

func TestQResyncSelectAndVanished(t *testing.T) {
	s := newQResyncServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	o := Options{Addr: s.ln.Addr().String(), TLS: TLSNone, Username: "u", Password: "p", QResync: true, CommandTimeout: 3 * time.Second}
	c, err := Dial(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Logout(context.Background()) }()
	if !c.QResync() {
		t.Fatal("QRESYNC must be enabled when offered and switched on")
	}

	info, qr, err := c.SelectQResync(ctx, "INBOX", true, 7, 42)
	if err != nil {
		t.Fatal(err)
	}
	if info.UIDValidity != 7 || info.HighestModSeq != 90 || info.Exists != 5 {
		t.Fatalf("select info: %+v", info)
	}
	if len(qr.Vanished) != 2 || qr.Vanished[0] != (UIDRange{2, 3}) || qr.Vanished[1] != (UIDRange{100, 4000000000}) {
		t.Fatalf("vanished ranges: %+v", qr.Vanished)
	}
	if len(qr.Changed) != 1 || qr.Changed[0].UID != 4 || qr.Changed[0].ModSeq != 80 || len(qr.Changed[0].Flags) != 1 {
		t.Fatalf("changed: %+v", qr.Changed)
	}
	var sawQResync bool
	for _, l := range s.lines() {
		if strings.HasSuffix(l, `EXAMINE INBOX (QRESYNC (7 42))`) {
			sawQResync = true
		}
	}
	if !sawQResync {
		t.Fatalf("no EXAMINE … (QRESYNC (7 42)) in %v", s.lines())
	}
	if got := c.UIDForSeq(4); got != 6 {
		t.Fatalf("seq map before VANISHED: seq 4 → %d", got)
	}

	// A live VANISHED replaces EXPUNGE: UIDs 5 and 6 leave the seq map, later sequence numbers shift down.
	if err := c.Noop(ctx); err != nil {
		t.Fatal(err)
	}
	if got := c.UIDForSeq(3); got != 7 {
		t.Fatalf("seq 3 after VANISHED 5:6 → %d, want 7", got)
	}
	var expunged []uint32
	deadline := time.After(3 * time.Second)
	for len(expunged) < 2 {
		select {
		case u := <-c.Updates():
			if u.Kind == UpdateExpunge {
				expunged = append(expunged, u.UID)
			}
		case <-deadline:
			t.Fatalf("expunge updates: %v", expunged)
		}
	}
	if expunged[0] != 5 || expunged[1] != 6 {
		t.Fatalf("expunged %v", expunged)
	}
}

func TestQResyncOffWhenNotSwitched(t *testing.T) {
	s := newQResyncServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := Dial(ctx, Options{Addr: s.ln.Addr().String(), TLS: TLSNone, Username: "u", Password: "p", CommandTimeout: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Logout(context.Background()) }()
	if c.QResync() {
		t.Fatal("QRESYNC must stay off unless the switch is on")
	}
	if _, _, err := c.SelectQResync(ctx, "INBOX", true, 7, 42); err == nil {
		t.Fatal("SelectQResync without QRESYNC must fail")
	}
	for _, l := range s.lines() {
		if strings.Contains(l, "ENABLE") {
			t.Fatalf("ENABLE sent without the switch: %q", l)
		}
	}
}
