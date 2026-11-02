package sysmail

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

func TestRenderAllKindsLocales(t *testing.T) {
	t.Parallel()
	kinds := []string{KindMFACode, KindInvitation, KindPasswordReset, KindEmailVerification, KindAlert}
	locales := []string{"en", "it"}
	data := map[string]string{
		"Code": "123456", "Name": "Ada", "InviteURL": "https://eb/i/x", "InviterName": "Bob",
		"OrgName": "Acme", "ResetURL": "https://eb/r/x", "VerifyURL": "https://eb/v/x",
		"Title": "Disk full", "Body": "Node n1 is out of disk.",
	}
	for _, kind := range kinds {
		for _, loc := range locales {
			r, err := Render(kind, loc, "EmailBridge <noreply@example.com>", "test@example.com", data)
			if err != nil {
				t.Fatalf("%s/%s: %v", kind, loc, err)
			}
			if r.Subject == "" || r.Text == "" || r.HTML == "" || len(r.Raw) == 0 {
				t.Fatalf("%s/%s empty", kind, loc)
			}
			if !strings.Contains(r.HTML, "EmailBridge") {
				t.Fatalf("%s/%s missing brand in HTML", kind, loc)
			}
			if !strings.Contains(string(r.Raw), "multipart/alternative") && !strings.Contains(string(r.Raw), "text/plain") {
				t.Fatalf("%s/%s MIME unexpected: %s", kind, loc, truncate(string(r.Raw), 200))
			}
			if kind == KindMFACode && !strings.Contains(r.Text, "123456") {
				t.Fatalf("code missing in text")
			}
		}
	}
}

func TestNormalizeLocale(t *testing.T) {
	t.Parallel()
	if NormalizeLocale("pt-BR") != "pt" || NormalizeLocale("") != "en" || NormalizeLocale("ja") != "ja" {
		t.Fatal(NormalizeLocale("pt-BR"), NormalizeLocale(""), NormalizeLocale("ja"))
	}
}

func TestRenderFallsBackToEnglish(t *testing.T) {
	t.Parallel()
	// zh templates may be missing until translation agents finish — must still render.
	r, err := Render(KindAlert, "zh", "noreply@example.com", "test@example.com", map[string]string{"Title": "T", "Body": "B"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Text == "" || r.HTML == "" {
		t.Fatal("empty fallback")
	}
}

func TestEverySupportedLocaleHasEveryTemplate(t *testing.T) {
	t.Parallel()
	kinds := []string{KindMFACode, KindInvitation, KindPasswordReset, KindEmailVerification, KindAlert}
	locales := []string{"en", "it", "es", "fr", "de", "pt", "ru", "zh", "ja"}
	var missing []string
	for _, loc := range locales {
		for _, kind := range kinds {
			if !HasTemplate(kind, loc) {
				missing = append(missing, loc+"/"+kind)
			}
		}
	}
	if len(missing) > 0 {
		t.Fatalf("missing templates (expected until W60 translators finish): %v", missing)
	}
}

func TestRenderUnknownKind(t *testing.T) {
	_, err := Render("nope", "en", "a@b", "c@d", nil)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestRecipientPolicy(t *testing.T) {
	t.Parallel()
	if !allowedRecipient("production", "anyone@x.com", nil) {
		t.Fatal("production must allow all")
	}
	if allowedRecipient("development", "evil@x.com", nil) {
		t.Fatal("dev must filter")
	}
	if allowedRecipient("staging", "test@example.com", nil) {
		t.Fatal("the default allow list is empty: nothing goes out outside production")
	}
	if !allowedRecipient("development", "dev@local", []string{"dev@local"}) {
		t.Fatal("custom allow")
	}
}

func TestRateLimit(t *testing.T) {
	t.Parallel()
	r := newRateLimiter()
	now := time.Now()
	for i := 0; i < rateLimitPerHour; i++ {
		if !r.allow("a@b.com", now) {
			t.Fatalf("token %d", i)
		}
	}
	if r.allow("a@b.com", now) {
		t.Fatal("11th must fail")
	}
	if !r.allow("other@b.com", now) {
		t.Fatal("other recipient independent")
	}
	if !r.allow("a@b.com", now.Add(rateLimitWindow+time.Second)) {
		t.Fatal("refilled after window")
	}
}

func TestEnqueueModes(t *testing.T) {
	t.Parallel()
	pub := &memPub{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	off := New(Options{Mode: ModeOff, Log: log}, pub)
	if err := off.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "test@example.com", Data: map[string]string{"Title": "t", "Body": "b"}}); err != nil {
		t.Fatal(err)
	}
	if pub.n != 0 {
		t.Fatal("off must not publish")
	}

	acc := New(Options{Mode: ModeAccount, Log: log}, pub)
	if err := acc.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "test@example.com"}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("got %v", err)
	}

	s := New(Options{Mode: ModeSMTP, From: "noreply@example.com", Env: "development", Log: log}, pub)
	if err := s.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "test@example.com", Locale: "en", Data: map[string]string{"Title": "t", "Body": "b"}}); err != nil {
		t.Fatal(err)
	}
	if pub.n != 1 || pub.lastID == "" {
		t.Fatalf("publish n=%d id=%q", pub.n, pub.lastID)
	}

	s.LocaleByEmail = func(_ context.Context, email string) string {
		if email != "test@example.com" {
			t.Fatalf("email %s", email)
		}
		return "ja-JP"
	}
	if err := s.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "test@example.com", Data: map[string]string{"Title": "t", "Body": "b"}}); err != nil {
		t.Fatal(err)
	}
	var j job
	if err := json.Unmarshal(pub.last, &j); err != nil {
		t.Fatal(err)
	}
	if j.Locale != "ja" {
		t.Fatalf("LocaleByEmail not applied: %q", j.Locale)
	}
}

type memPub struct {
	n      int
	lastID string
	last   []byte
}

func (m *memPub) Publish(_ context.Context, _ string, data []byte, msgID string, _ map[string]string) (bool, error) {
	m.n++
	m.lastID = msgID
	m.last = append([]byte(nil), data...)
	return false, nil
}

func TestWorkerDeliveryAndPolicy(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sent []byte
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Options{Mode: ModeSMTP, From: "EmailBridge <noreply@example.com>", Env: "development", AllowList: []string{"test@example.com"}, Log: log}, &memPub{})
	s.send = func(_ context.Context, _ smtpc.Options, _ string, to []string, raw []byte) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append([]byte(nil), raw...)
		if len(to) != 1 || to[0] != "test@example.com" {
			t.Errorf("to %v", to)
		}
		return nil
	}
	j, _ := json.Marshal(job{ID: "job_1", Kind: KindAlert, To: "evil@x.com", Locale: "en", Data: map[string]string{"Title": "t", "Body": "b"}})
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if sent != nil {
		t.Fatal("policy should drop")
	}
	mu.Unlock()

	j, _ = json.Marshal(job{ID: "job_2", Kind: KindMFACode, To: "test@example.com", Locale: "en", Data: map[string]string{"Code": "999999"}})
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(string(sent), "999999") {
		t.Fatalf("sent %s", truncate(string(sent), 300))
	}
}

func TestWorkerRetryOnTemporary(t *testing.T) {
	t.Parallel()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Options{Mode: ModeSMTP, From: "a@b", Env: "production", Log: log}, &memPub{})
	s.send = func(context.Context, smtpc.Options, string, []string, []byte) error {
		return errs.New(errs.CodeSMTPTemporary, "450")
	}
	j, _ := json.Marshal(job{ID: "job_3", Kind: KindAlert, To: "x@y.com", Locale: "en", Data: map[string]string{"Title": "t", "Body": "b"}})
	err := s.HandleJob(context.Background(), j)
	if !errs.Retryable(err) {
		t.Fatalf("want retryable, got %v", err)
	}
}

func TestWorkerWithEmbeddedNATSAndFakeSMTP(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	received := make(chan string, 1)
	go acceptOneSMTP(ln, received)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	port := ln.Addr().(*net.TCPAddr).Port
	s := New(Options{
		Mode: ModeSMTP, From: "noreply@example.com", Env: "production", Log: log,
		SMTP: smtpc.Options{Host: "127.0.0.1", Port: port, TLS: smtpc.TLSNone, EHLO: "eb"},
	}, b)
	stop, err := s.StartWorker(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)

	if err := s.Enqueue(context.Background(), Mail{
		Kind: KindAlert, To: "user@example.com", Locale: "en",
		Data: map[string]string{"Title": "Hello", "Body": "World"},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-received:
		if !strings.Contains(body, "Hello") {
			t.Fatalf("body %q", truncate(body, 400))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for SMTP delivery")
	}
}

func acceptOneSMTP(ln net.Listener, out chan<- string) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) { _, _ = w.WriteString(s); _ = w.Flush() }
	write("220 test ESMTP\r\n")
	var data strings.Builder
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		upper := strings.ToUpper(strings.TrimRight(line, "\r\n"))
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			write("250-localhost\r\n250 OK\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"), strings.HasPrefix(upper, "RCPT TO:"):
			write("250 OK\r\n")
		case upper == "DATA":
			write("354 Go\r\n")
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				data.WriteString(l)
			}
			write("250 OK\r\n")
			out <- data.String()
		case upper == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
