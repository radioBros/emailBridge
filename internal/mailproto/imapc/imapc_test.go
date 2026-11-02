package imapc

import (
	"bufio"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/protolog"
)

// fakeServer is a minimal scripted IMAP server for behaviours a real server will not show on demand
// (a DONE that is never answered, LOGIN rejected).
type fakeServer struct {
	ln          net.Listener
	answerDone  bool
	rejectLogin bool
	idleLine    string // sent right after "+ idling" when set
	mu          sync.Mutex
	got         []string
}

func newFake(t *testing.T, answerDone, rejectLogin bool) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, answerDone: answerDone, rejectLogin: rejectLogin}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeServer) serve() {
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(c)
	}
}

func (f *fakeServer) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	w := func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
	w("* OK [CAPABILITY IMAP4rev1 IDLE ID] fake ready")
	r := bufio.NewReader(c)
	idleTag := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.mu.Lock()
		f.got = append(f.got, line)
		f.mu.Unlock()
		if line == "DONE" {
			if f.answerDone {
				w(idleTag + " OK idle done")
			}
			continue
		}
		tag, rest, _ := strings.Cut(line, " ")
		cmd := strings.ToUpper(strings.Fields(rest + " x")[0])
		switch cmd {
		case "CAPABILITY":
			w("* CAPABILITY IMAP4rev1 IDLE ID")
			w(tag + " OK done")
		case "LOGIN":
			if f.rejectLogin {
				w(tag + " NO [AUTHENTICATIONFAILED] invalid credentials")
			} else {
				w(tag + " OK [CAPABILITY IMAP4rev1 IDLE ID] logged in")
			}
		case "ID":
			w(`* ID ("name" "fake")`)
			w(tag + " OK id")
		case "SELECT", "EXAMINE":
			w("* 0 EXISTS")
			w("* OK [UIDVALIDITY 7] ok")
			w("* OK [UIDNEXT 1] ok")
			w(tag + " OK [READ-WRITE] selected")
		case "IDLE":
			idleTag = tag
			w("+ idling")
			if f.idleLine != "" {
				w(f.idleLine)
			}
		case "STARTTLS":
			w(tag + " NO STARTTLS not available")
		case "NOOP":
			w(tag + " OK noop")
		case "LOGOUT":
			w("* BYE")
			w(tag + " OK bye")
			return
		default:
			w(tag + " BAD unknown")
		}
	}
}

func fakeOpts(f *fakeServer) Options {
	return Options{Addr: f.ln.Addr().String(), TLS: TLSNone, Username: "u", Password: "p", ID: map[string]string{"name": "EmailBridge"}, DoneTimeout: 500 * time.Millisecond, CommandTimeout: 3 * time.Second}
}

func TestDoneTimeoutDetectsDeadConnection(t *testing.T) {
	f := newFake(t, false, false)
	ctx := context.Background()
	c, err := Dial(ctx, fakeOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Has("idle") || c.Caps()["IDLE"] != true {
		t.Fatalf("caps %v", c.Caps())
	}
	if _, err := c.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err = c.Idle(ctx, 50*time.Millisecond)
	if errs.CodeOf(err) != errs.CodeMailTimeout {
		t.Fatalf("want mail_timeout, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("DONE timeout took too long")
	}
	select {
	case <-c.Closed():
	case <-time.After(2 * time.Second):
		t.Fatal("connection must be closed after a dead DONE")
	}
	if err := c.Noop(ctx); err == nil {
		t.Fatal("commands on a closed client must fail")
	}
}

func TestIdleCycleAndLogout(t *testing.T) {
	f := newFake(t, true, false)
	ctx := context.Background()
	var tapped []protolog.Line
	var mu sync.Mutex
	o := fakeOpts(f)
	o.Tap = func(l protolog.Line) { mu.Lock(); tapped = append(tapped, l); mu.Unlock() }
	o.TapConn = "acc_1/main"
	c, err := Dial(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Select(ctx, "INBOX", true); err != nil {
		t.Fatal(err)
	}
	if c.Selected() != "INBOX" {
		t.Fatal("selected")
	}
	if err := c.Idle(ctx, 50*time.Millisecond); err != nil {
		t.Fatalf("idle cycle: %v", err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Idle(cctx, time.Hour); err != nil && errs.CodeOf(err) != errs.CodeMailTimeout {
		t.Logf("cancelled idle: %v", err)
	}
	_ = c.Logout(ctx)
	mu.Lock()
	defer mu.Unlock()
	joined := ""
	for _, l := range tapped {
		joined += string(l.Dir) + ":" + l.Text + "\n"
	}
	if !strings.Contains(joined, "LOGIN [masked]") || strings.Contains(joined, `"p"`) || !strings.Contains(joined, "IDLE") {
		t.Fatalf("tap:\n%s", joined)
	}
}

func TestLoginRejectedIsAuth(t *testing.T) {
	f := newFake(t, true, true)
	_, err := Dial(context.Background(), fakeOpts(f))
	if errs.CodeOf(err) != errs.CodeMailAuthFailed || errs.KindOf(err) != errs.KindAuth {
		t.Fatalf("want mail_auth_failed, got %v (%s)", err, errs.KindOf(err))
	}
}

func TestDialErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Dial(ctx, Options{Addr: "no-port"}); err == nil {
		t.Fatal("bad address")
	}
	_, err := Dial(ctx, Options{Addr: "127.0.0.1:1", TLS: TLSNone})
	if errs.CodeOf(err) != errs.CodeMailConnectFailed {
		t.Fatalf("refused: %v", err)
	}
	// implicit TLS against a plain server → TLS failure
	f := newFake(t, true, false)
	o := fakeOpts(f)
	o.TLS = TLSImplicit
	_, err = Dial(ctx, o)
	if c := errs.CodeOf(err); c != errs.CodeMailTLSFailed && c != errs.CodeMailConnectFailed && c != errs.CodeMailTimeout {
		t.Fatalf("tls against plain: %v", err)
	}
}

func TestHugeExistsDoesNotAllocate(t *testing.T) {
	f := newFake(t, true, false)
	f.idleLine = "* 4000000000 EXISTS"
	ctx := context.Background()
	c, err := Dial(ctx, fakeOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Idle(ctx, 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	select {
	case u := <-c.Updates():
		if u.Kind != UpdateOverflow || u.NumMessages != 4000000000 {
			t.Fatalf("update %+v", u)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no update")
	}
	if c.UIDForSeq(1) != 0 {
		t.Fatal("seq map must be empty")
	}
}

func TestStartTLSRefused(t *testing.T) {
	f := newFake(t, true, false)
	o := fakeOpts(f)
	o.TLS = TLSStartTLS
	_, err := Dial(context.Background(), o)
	if errs.CodeOf(err) != errs.CodeMailTLSFailed {
		t.Fatalf("want mail_tls_failed, got %v", err)
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		op   string
		code errs.Code
		kind errs.Kind
	}{
		{&imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeAuthenticationFailed}, "login", errs.CodeMailAuthFailed, errs.KindAuth},
		{&imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeUnavailable}, "select", errs.CodeMailConnectFailed, ""},
		{&imap.Error{Type: imap.StatusResponseTypeNo, Code: imap.ResponseCodeLimit}, "login", errs.CodeMailConnectFailed, errs.KindRateLimited},
		{&imap.Error{Type: imap.StatusResponseTypeNo, Text: "nope"}, "login", errs.CodeMailAuthFailed, ""},
		{&imap.Error{Type: imap.StatusResponseTypeBad, Text: "parse"}, "fetch", errs.CodeMailProtocolError, ""},
		{x509.UnknownAuthorityError{}, "tls", errs.CodeMailTLSFailed, ""},
		{errors.New("tls: handshake failure"), "tls", errs.CodeMailTLSFailed, ""},
		{context.DeadlineExceeded, "fetch", errs.CodeMailTimeout, errs.KindTemporary},
		{ErrDead, "idle", errs.CodeMailTimeout, ""},
		{io.EOF, "fetch", errs.CodeMailConnectFailed, ""},
		{&net.OpError{Op: "dial", Err: errors.New("x")}, "dial", errs.CodeMailConnectFailed, ""},
		{errors.New("weird"), "fetch", errs.CodeMailProtocolError, ""},
	}
	for _, c := range cases {
		got := classify(c.op, c.err)
		if errs.CodeOf(got) != c.code || (c.kind != "" && errs.KindOf(got) != c.kind) {
			t.Errorf("classify(%v) = %s/%s, want %s/%s", c.err, errs.CodeOf(got), errs.KindOf(got), c.code, c.kind)
		}
	}
	if classify("x", nil) != nil {
		t.Fatal("nil")
	}
	pre := errs.New(errs.CodeMailTimeout, "x")
	if got := classify("x", pre); !errors.Is(got, pre) || got != error(pre) { //nolint:errorlint // identity is the point: classified errors pass through unchanged
		t.Fatal("already classified errors pass through")
	}
	if PathString([]int{1, 2, 3}) != "1.2.3" {
		t.Fatal("PathString")
	}
}

func TestIdleUntilWake(t *testing.T) {
	f := newFake(t, true, false)
	ctx := context.Background()
	c, err := Dial(ctx, fakeOpts(f))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.Select(ctx, "INBOX", true); err != nil {
		t.Fatal(err)
	}
	wake := make(chan struct{})
	close(wake) // already woken before IDLE starts: the cycle must still end cleanly, connection intact
	start := time.Now()
	if err := c.IdleUntil(ctx, time.Hour, wake); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("wake ignored")
	}
	if err := c.Noop(ctx); err != nil {
		t.Fatalf("connection must survive a wake: %v", err)
	}
}
