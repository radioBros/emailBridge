package imapclient

// EmailBridge patch tests: QRESYNC encoding and VANISHED parsing against a scripted server.

import (
	"bufio"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

type scripted struct {
	t     *testing.T
	conn  net.Conn
	r     *bufio.Reader
	lines chan string
}

func (s *scripted) send(lines ...string) {
	for _, l := range lines {
		if _, err := s.conn.Write([]byte(l + "\r\n")); err != nil {
			s.t.Errorf("server write: %v", err)
		}
	}
}

// next returns the next command line the client sent.
func (s *scripted) next() string {
	select {
	case l := <-s.lines:
		return l
	case <-time.After(5 * time.Second):
		s.t.Fatal("no command from the client")
		return ""
	}
}

func newScripted(t *testing.T, handler *UnilateralDataHandler) (*Client, *scripted) {
	t.Helper()
	srvConn, cliConn := net.Pipe()
	s := &scripted{t: t, conn: srvConn, r: bufio.NewReader(srvConn), lines: make(chan string, 16)}
	go func() {
		for {
			l, err := s.r.ReadString('\n')
			if err != nil {
				close(s.lines)
				return
			}
			s.lines <- strings.TrimRight(l, "\r\n")
		}
	}()
	go s.send("* OK [CAPABILITY IMAP4rev1 CONDSTORE QRESYNC ENABLE] ready")
	c := New(cliConn, &Options{UnilateralDataHandler: handler})
	t.Cleanup(func() { _ = c.Close(); _ = srvConn.Close() })
	if err := c.WaitGreeting(); err != nil {
		t.Fatal(err)
	}
	return c, s
}

func tagOf(line string) string { return strings.SplitN(line, " ", 2)[0] }

func TestQResyncSelectFetchAndVanished(t *testing.T) {
	var mu sync.Mutex
	var got []VanishedData
	var fetched []uint32
	fetchDone := make(chan struct{}, 1)
	handler := &UnilateralDataHandler{
		Vanished: func(d *VanishedData) { mu.Lock(); got = append(got, *d); mu.Unlock() },
		Fetch: func(msg *FetchMessageData) {
			for item := msg.Next(); item != nil; item = msg.Next() {
				if uid, ok := item.(FetchItemDataUID); ok {
					mu.Lock()
					fetched = append(fetched, uint32(uid.UID))
					mu.Unlock()
				}
			}
			fetchDone <- struct{}{}
		},
	}
	c, s := newScripted(t, handler)

	enable := c.Enable(imap.CapQResync)
	line := s.next()
	if !strings.HasSuffix(line, " ENABLE QRESYNC") {
		t.Fatalf("enable: %q", line)
	}
	s.send("* ENABLED QRESYNC", tagOf(line)+" OK enabled")
	if _, err := enable.Wait(); err != nil {
		t.Fatal(err)
	}

	var known imap.UIDSet
	known.AddRange(1, 20)
	sel := c.Select("INBOX", &imap.SelectOptions{QResync: &imap.SelectQResync{UIDValidity: 7, ModSeq: 42, KnownUIDs: known}})
	line = s.next()
	if !strings.HasSuffix(line, ` SELECT INBOX (QRESYNC (7 42 1:20))`) {
		t.Fatalf("select: %q", line)
	}
	s.send("* 10 EXISTS", "* OK [UIDVALIDITY 7] ok", "* OK [UIDNEXT 21] ok", "* OK [HIGHESTMODSEQ 90] ok",
		"* VANISHED (EARLIER) 3:5,9", "* 1 FETCH (UID 2 FLAGS (\\Seen) MODSEQ (50))", tagOf(line)+" OK [READ-WRITE] selected")
	data, err := sel.Wait()
	if err != nil || data.NumMessages != 10 || data.HighestModSeq != 90 {
		t.Fatalf("select data: %+v %v", data, err)
	}
	if c.Mailbox().NumMessages != 10 {
		t.Fatalf("EARLIER must not change the count: %d", c.Mailbox().NumMessages)
	}

	// While selected, VANISHED without EARLIER replaces EXPUNGE: the count drops by the number of UIDs.
	noop := c.Noop()
	line = s.next()
	s.send("* VANISHED 6:7", tagOf(line)+" OK noop")
	if err := noop.Wait(); err != nil {
		t.Fatal(err)
	}
	if c.Mailbox().NumMessages != 8 {
		t.Fatalf("count after VANISHED 6:7: %d", c.Mailbox().NumMessages)
	}

	var all imap.UIDSet
	all.AddRange(1, 0)
	fetch := c.Fetch(all, &imap.FetchOptions{Flags: true, ChangedSince: 42, Vanished: true})
	line = s.next()
	if !strings.HasSuffix(line, " UID FETCH 1:* (UID FLAGS) (CHANGEDSINCE 42 VANISHED)") {
		t.Fatalf("uid fetch: %q", line)
	}
	s.send("* VANISHED (EARLIER) 11", tagOf(line)+" OK fetched")
	if err := fetch.Close(); err != nil {
		t.Fatal(err)
	}

	select { // the unilateral FETCH handler may still be draining its items
	case <-fetchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("unilateral FETCH handler never finished")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || !got[0].Earlier || got[0].UIDs.String() != "3:5,9" || got[1].Earlier ||
		got[1].UIDs.String() != "6:7" || !got[2].Earlier || got[2].UIDs.String() != "11" {
		t.Fatalf("vanished: %+v", got)
	}
	if len(fetched) != 1 || fetched[0] != 2 {
		t.Fatalf("unilateral FETCH during SELECT: %v", fetched)
	}
}

func TestVanishedRejectsUnknownTag(t *testing.T) {
	c, s := newScripted(t, &UnilateralDataHandler{Vanished: func(*VanishedData) {}})
	noop := c.Noop()
	line := s.next()
	_ = tagOf(line)
	s.send("* VANISHED (LATER) 1") // the client drops the connection on this protocol error
	if err := noop.Wait(); err == nil {
		t.Fatal("an unknown VANISHED tag must be a protocol error")
	}
}

func TestUIDSetCount(t *testing.T) {
	var set imap.UIDSet
	set.AddRange(3, 5)
	set.AddNum(9)
	set = append(set, imap.UIDRange{Start: 20, Stop: 18})
	if n := uidSetCount(set); n != 7 {
		t.Fatalf("count %d", n)
	}
}
