//go:build integration

package imapc

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/radiobros/emailbridge/internal/protolog"
	"github.com/radiobros/emailbridge/test/containers"
)

// dovecot returns the test server: EB_TEST_IMAP_ADDR (+ _STARTTLS_ADDR, _PASSWORD) when set, else the
// compose dev stack when it is up, else one container shared by the package's tests.
func dovecot(t *testing.T) containers.DovecotAddrs {
	t.Helper()
	if a := os.Getenv("EB_TEST_IMAP_ADDR"); a != "" {
		pass := os.Getenv("EB_TEST_IMAP_PASSWORD")
		if pass == "" {
			pass = "devpass"
		}
		return containers.DovecotAddrs{IMAPS: a, IMAP: os.Getenv("EB_TEST_IMAP_STARTTLS_ADDR"), Password: pass}
	}
	if a, ok := containers.DovecotCompose(); ok {
		return a
	}
	return containers.DovecotShared(t)
}

func dovecotOpts(t *testing.T, user string) Options {
	t.Helper()
	d := dovecot(t)
	o := Options{Addr: d.IMAPS, TLS: TLSImplicit, InsecureSkipVerify: true, Username: user, Password: d.Password, ID: map[string]string{"name": "EmailBridge", "version": "test"}}
	if os.Getenv("EB_TEST_TAP") == "1" { // print the masked transcript while debugging
		o.Tap = func(l protolog.Line) { t.Logf("%c %s", l.Dir, l.Text) }
		o.TapMode = protolog.Raw
	}
	return o
}

func TestIntegrationStartTLS(t *testing.T) {
	d := dovecot(t)
	if d.IMAP == "" {
		t.Skip("EB_TEST_IMAP_STARTTLS_ADDR not set")
	}
	var lines []string
	var mu sync.Mutex
	o := dovecotOpts(t, "starttls-user")
	o.Addr, o.TLS = d.IMAP, TLSStartTLS
	o.Tap = func(l protolog.Line) { mu.Lock(); lines = append(lines, string(l.Dir)+" "+l.Text); mu.Unlock() }
	c, err := Dial(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if c.Has("STARTTLS") || !c.Has("IDLE") {
		t.Fatalf("capabilities must be the post-TLS set: %v", c.Caps())
	}
	if _, err := c.Select(context.Background(), "INBOX", true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	all := strings.Join(lines, "\n")
	if !strings.Contains(all, "STARTTLS") || !strings.Contains(all, "SELECT") || strings.Contains(all, d.Password) {
		t.Fatalf("transcript must be plaintext, masked:\n%s", all)
	}
	for _, l := range lines {
		if !utf8.ValidString(l) {
			t.Fatalf("ciphertext in the tap: %q", l)
		}
	}
}

func msg(subject, msgid string) []byte {
	return []byte(fmt.Sprintf("From: Mario <mario@example.test>\r\nTo: Team <team@example.test>\r\nSubject: %s\r\nMessage-ID: %s\r\nX-Ricevuta: accettazione\r\nDate: %s\r\nContent-Type: multipart/mixed; boundary=b1\r\n\r\n--b1\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello body\r\n--b1\r\nContent-Type: application/pdf; name=a.pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0=\r\n--b1--\r\n",
		subject, msgid, time.Now().Format(time.RFC1123Z)))
}

func waitUpdate(t *testing.T, c *Client, kind UpdateKind) Update {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case u := <-c.Updates():
			if u.Kind == kind {
				return u
			}
		case <-timeout:
			t.Fatalf("no update of kind %d", kind)
		}
	}
}

func TestIntegrationDovecot(t *testing.T) {
	user := fmt.Sprintf("it%d", time.Now().UnixNano())
	ctx := context.Background()
	c1, err := Dial(ctx, dovecotOpts(t, user))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c1.Close() }()
	c2, err := Dial(ctx, dovecotOpts(t, user))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c2.Close() }()
	for _, cp := range []string{"IDLE", "CONDSTORE", "SPECIAL-USE", "MOVE", "UIDPLUS", "LIST-STATUS"} {
		if !c1.Has(cp) {
			t.Errorf("missing capability %s", cp)
		}
	}
	list, err := c1.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	special := map[string]string{}
	for _, m := range list {
		if m.SpecialUse != "" {
			special[m.SpecialUse] = m.Name
		}
		if m.Status == nil {
			t.Errorf("no status for %s", m.Name)
		}
	}
	if special[`\Sent`] == "" || special[`\Trash`] == "" {
		t.Fatalf("special-use: %v", special)
	}
	sel, err := c1.Select(ctx, "INBOX", false)
	if err != nil {
		t.Fatal(err)
	}
	if sel.UIDValidity == 0 || sel.UIDNext == 0 {
		t.Fatalf("select %+v", sel)
	}
	baseModseq := sel.HighestModSeq
	// idle in the background on c1 while c2 appends
	idleCtx, stopIdle := context.WithCancel(ctx)
	idleDone := make(chan error, 1)
	go func() { idleDone <- c1.Idle(idleCtx, time.Minute) }()
	time.Sleep(200 * time.Millisecond)
	uid1, err := c2.Append(ctx, "INBOX", nil, time.Now(), msg("first", "<m1@example.test>"))
	if err != nil || uid1 == 0 {
		t.Fatalf("append: %d %v", uid1, err)
	}
	u := waitUpdate(t, c1, UpdateExists)
	if u.NumMessages != 1 {
		t.Fatalf("exists %+v", u)
	}
	stopIdle()
	if err := <-idleDone; err != nil {
		t.Fatalf("idle: %v", err)
	}
	msgs, err := c1.Fetch(ctx, sel.UIDNext, 0, FetchOptions{Flags: true, Envelope: true, Body: true, InternalDate: true, Size: true, ModSeq: true, HeaderFields: []string{"X-Ricevuta", "Message-ID"}})
	if err != nil || len(msgs) != 1 {
		t.Fatalf("fetch: %d %v", len(msgs), err)
	}
	m := msgs[0]
	switch {
	case m.UID != uid1:
		t.Fatalf("uid %d want %d", m.UID, uid1)
	case m.Envelope == nil || m.Envelope.Subject != "first" || len(m.Envelope.From) != 1 || m.Envelope.From[0].Address != "mario@example.test" || m.Envelope.MessageID != "<m1@example.test>":
		t.Fatalf("envelope %+v", m.Envelope)
	case !strings.Contains(string(m.Header), "accettazione"):
		t.Fatalf("header %q", m.Header)
	case m.Body == nil || len(m.Body.Children) != 2 || m.Body.Children[1].Filename != "a.pdf" || m.Body.Children[1].Disposition != "attachment":
		t.Fatalf("body %+v / %+v", m.Body, m.Body.Children[1])
	case m.Size == 0 || m.InternalDate.IsZero():
		t.Fatalf("size/date %d %v", m.Size, m.InternalDate)
	}
	if c1.UIDForSeq(1) != uid1 {
		t.Fatal("seq map not filled by fetch")
	}
	text, err := c1.Section(ctx, uid1, []int{1}, 0)
	if err != nil || !strings.Contains(string(text), "hello body") {
		t.Fatalf("section: %q %v", text, err)
	}
	raw, err := c1.Raw(ctx, uid1, 0)
	if err != nil || !strings.Contains(string(raw), "Subject: first") {
		t.Fatalf("raw: %v", err)
	}
	// flag change seen via IDLE with UID resolved from the seq map (SP1: FETCH carries no UID)
	idleCtx, stopIdle = context.WithCancel(ctx)
	go func() { idleDone <- c1.Idle(idleCtx, time.Minute) }()
	time.Sleep(200 * time.Millisecond)
	if _, err := c2.Select(ctx, "INBOX", false); err != nil {
		t.Fatal(err)
	}
	if err := c2.StoreFlags(ctx, []uint32{uid1}, true, []string{`\Seen`}); err != nil {
		t.Fatal(err)
	}
	fu := waitUpdate(t, c1, UpdateFlags)
	if fu.UID != uid1 || !strings.Contains(strings.Join(fu.Flags, " "), `\Seen`) {
		t.Fatalf("flags update %+v", fu)
	}
	// move out â†’ expunge with UID
	if err := c2.Move(ctx, []uint32{uid1}, special[`\Trash`]); err != nil {
		t.Fatal(err)
	}
	eu := waitUpdate(t, c1, UpdateExpunge)
	if eu.UID != uid1 {
		t.Fatalf("expunge update %+v", eu)
	}
	stopIdle()
	if err := <-idleDone; err != nil {
		t.Fatalf("idle: %v", err)
	}
	// CONDSTORE changed-since on Trash; search by Message-ID; UIDs; delete
	if _, err := c1.Select(ctx, special[`\Trash`], false); err != nil {
		t.Fatal(err)
	}
	changed, err := c1.Fetch(ctx, 1, 0, FetchOptions{Flags: true, ModSeq: true, ChangedSince: baseModseq})
	if err != nil || len(changed) != 1 {
		t.Fatalf("changed since: %d %v", len(changed), err)
	}
	found, err := c1.SearchMessageID(ctx, "<m1@example.test>")
	if err != nil || len(found) != 1 {
		t.Fatalf("search: %v %v", found, err)
	}
	all, err := c1.UIDs(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("uids: %v %v", all, err)
	}
	if err := c1.StoreFlags(ctx, all, true, []string{`\Deleted`}); err != nil {
		t.Fatal(err)
	}
	if err := c1.Expunge(ctx, all); err != nil {
		t.Fatal(err)
	}
	if left, _ := c1.UIDs(ctx); len(left) != 0 {
		t.Fatalf("expunge left %v", left)
	}
	st, err := c1.Status(ctx, "INBOX")
	if err != nil || st.UIDNext <= uid1 {
		t.Fatalf("status %+v %v", st, err)
	}
	if err := c1.Noop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c1.Logout(ctx); err != nil {
		t.Logf("logout: %v", err)
	}
}

// TestIntegrationDovecotQResync runs the QRESYNC flow on a real server: changes and expunges made by another
// connection come back from SELECT … (QRESYNC (uidvalidity modseq)), and a live expunge arrives as VANISHED.
func TestIntegrationDovecotQResync(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	user := fmt.Sprintf("qr%d", time.Now().UnixNano())
	o := dovecotOpts(t, user)
	o.QResync = true
	a, err := Dial(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Logout(context.Background()) }()
	if !a.Has("QRESYNC") {
		t.Skip("server does not offer QRESYNC")
	}
	if !a.QResync() {
		t.Fatal("ENABLE QRESYNC failed")
	}
	b, err := Dial(ctx, dovecotOpts(t, user)) // the "other client" that changes the mailbox
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = b.Logout(context.Background()) }()

	box := "QR"
	if err := b.Create(ctx, box); err != nil {
		t.Fatal(err)
	}
	var uids []uint32
	for i := range 3 {
		raw := []byte(fmt.Sprintf("From: a@x.test\r\nTo: b@x.test\r\nSubject: qr %d\r\nMessage-ID: <qr-%d-%s@x.test>\r\n\r\nbody\r\n", i, i, user))
		uid, err := b.Append(ctx, box, nil, time.Now(), raw)
		if err != nil {
			t.Fatal(err)
		}
		uids = append(uids, uid)
	}
	before, err := a.Select(ctx, box, true)
	if err != nil {
		t.Fatal(err)
	}
	if before.HighestModSeq == 0 {
		t.Fatal("no HIGHESTMODSEQ")
	}

	if _, err := b.Select(ctx, box, false); err != nil {
		t.Fatal(err)
	}
	if err := b.StoreFlags(ctx, uids[:1], true, []string{`\Seen`}); err != nil {
		t.Fatal(err)
	}
	if err := b.StoreFlags(ctx, uids[1:2], true, []string{`\Deleted`}); err != nil {
		t.Fatal(err)
	}
	if err := b.Expunge(ctx, uids[1:2]); err != nil {
		t.Fatal(err)
	}

	info, qr, err := a.SelectQResync(ctx, box, true, before.UIDValidity, before.HighestModSeq)
	if err != nil {
		t.Fatal(err)
	}
	if info.UIDValidity != before.UIDValidity || info.HighestModSeq <= before.HighestModSeq {
		t.Fatalf("select: %+v before %+v", info, before)
	}
	var seen bool
	for _, m := range qr.Changed {
		if m.UID == uids[0] {
			for _, f := range m.Flags {
				seen = seen || f == `\Seen`
			}
		}
	}
	if !seen {
		t.Fatalf("changed since %d: %+v", before.HighestModSeq, qr.Changed)
	}
	var vanished bool
	for _, r := range qr.Vanished {
		vanished = vanished || (uids[1] >= r.Start && uids[1] <= r.Stop)
	}
	if !vanished {
		t.Fatalf("vanished: %+v (want %d)", qr.Vanished, uids[1])
	}

	// Live: an expunge by the other client while a has the folder selected arrives as VANISHED.
	if err := b.StoreFlags(ctx, uids[2:3], true, []string{`\Deleted`}); err != nil {
		t.Fatal(err)
	}
	if err := b.Expunge(ctx, uids[2:3]); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if err := a.Noop(ctx); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case u := <-a.Updates():
				if u.Kind == UpdateExpunge && u.UID == uids[2] {
					return
				}
				continue
			default:
			}
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("no expunge update for UID %d", uids[2])
}
