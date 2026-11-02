package protolog

import (
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

type rec struct {
	mu    sync.Mutex
	lines []Line
}

func (r *rec) sink(l Line) {
	r.mu.Lock()
	r.lines = append(r.lines, l)
	r.mu.Unlock()
}

func (r *rec) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, l := range r.lines {
		b.WriteByte(l.Dir)
		b.WriteString(": ")
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// session plays server lines into the tapped conn while the test writes client lines.
func run(t *testing.T, proto string, mode Mode, script []string) string {
	t.Helper()
	a, b := net.Pipe()
	r := &rec{}
	c := Wrap(a, proto, "c1", mode, r.sink)
	done := make(chan struct{})
	go func() { // drain what the client writes
		_, _ = io.Copy(io.Discard, b)
		close(done)
	}()
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			_ = n
			if err != nil {
				return
			}
		}
	}()
	for _, step := range script {
		dir, payload := step[0], step[2:]
		payload = strings.ReplaceAll(payload, `\n`, "\r\n")
		if dir == 'C' {
			if _, err := c.Write([]byte(payload)); err != nil {
				t.Fatal(err)
			}
		} else if _, err := b.Write([]byte(payload)); err != nil { // returns once the tapped side read it
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	_ = c.Close()
	_ = b.Close()
	<-done
	return r.text()
}

func TestIMAPMaskingAndLiterals(t *testing.T) {
	out := run(t, "imap", Normal, []string{
		`S * OK ready\n`,
		`C a1 LOGIN "mario@example.com" "s3cr3t"\n`,
		`S a1 OK logged in\n`,
		`C a2 AUTHENTICATE PLAIN dGVzdAB0ZXN0AHBhc3M=\n`,
		`S a2 OK\n`,
		`C a3 AUTHENTICATE XOAUTH2\n`,
		`S + \n`,
		`C dXNlcj1tYXJpbwFhdXRoPUJlYXJlciB0b2tlbgEB\n`,
		`S a3 OK\n`,
		`C a4 UID FETCH 1 (BODY.PEEK[])\n`,
		`S * 1 FETCH (UID 1 BODY[] {11}\nhello world)\n`,
		`S a4 OK done\n`,
		`C a5 APPEND Sent {5}\n`,
		`C hello\n`,
	})
	for _, secret := range []string{"s3cr3t", "dGVzdAB0ZXN0AHBhc3M", "dXNlcj1tYXJpbw", "hello world"} {
		if strings.Contains(out, secret) {
			t.Fatalf("leaked %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{"C: a1 LOGIN [masked]", "AUTHENTICATE PLAIN [masked]", "AUTHENTICATE XOAUTH2 [masked]", "{literal 11 bytes}", "S: a4 OK done"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestSMTPMaskingAndData(t *testing.T) {
	out := run(t, "smtp", Normal, []string{
		`S 220 mx ESMTP\n`,
		`C EHLO me\n`,
		`S 250 ok\n`,
		`C AUTH LOGIN\n`,
		`S 334 VXNlcm5hbWU6\n`,
		`C bWFyaW8=\n`,
		`S 334 UGFzc3dvcmQ6\n`,
		`C c2VjcmV0\n`,
		`S 235 ok\n`,
		`C MAIL FROM:<a@b>\n`,
		`S 250 ok\n`,
		`C DATA\n`,
		`S 354 go\n`,
		`C Subject: private\nbody line\n.\n`,
		`S 250 queued\n`,
		`C QUIT\n`,
	})
	for _, secret := range []string{"bWFyaW8", "c2VjcmV0", "private", "body line"} {
		if strings.Contains(out, secret) {
			t.Fatalf("leaked %q:\n%s", secret, out)
		}
	}
	for _, want := range []string{"C: AUTH LOGIN [masked]", "C: MAIL FROM:<a@b>", "{data ", "C: QUIT"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	// AUTH PLAIN with initial response: the next command must not be masked
	out = run(t, "smtp", Normal, []string{`C AUTH PLAIN AGEAYg==\n`, `S 235 ok\n`, `C MAIL FROM:<x@y>\n`})
	if strings.Contains(out, "AGEAYg") || !strings.Contains(out, "C: MAIL FROM:<x@y>") {
		t.Fatalf("plain ir:\n%s", out)
	}
}

func TestRawModeKeepsContentButMasksCredentials(t *testing.T) {
	out := run(t, "imap", Raw, []string{
		`C a1 LOGIN u p4ss\n`,
		`S a1 OK\n`,
		`S * 1 FETCH (BODY[] {5}\nhello)\n`,
	})
	if strings.Contains(out, "p4ss") || !strings.Contains(out, "hello") {
		t.Fatalf("raw:\n%s", out)
	}
	long := strings.Repeat("x", 3000)
	out = run(t, "imap", Normal, []string{`S * OK ` + long + `\n`})
	if !strings.Contains(out, "…") || len(out) > 2200 {
		t.Fatalf("long line not truncated: %d", len(out))
	}
}
