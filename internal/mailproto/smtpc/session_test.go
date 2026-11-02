package smtpc

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/protolog"
)

// srv is a scripted go-smtp server. Behaviour per recipient prefix and per data mode.
type srv struct {
	mu        sync.Mutex
	dataMode  string // "", "452", "554", "close", "hang", "refuse-data"
	mails     int
	datas     int
	bodies    []string
	authMechs []string
	release   chan struct{}
}

type sess struct {
	s    *srv
	conn *smtp.Conn
}

func (s *sess) Reset()        {}
func (s *sess) Logout() error { return nil }
func (s *sess) Mail(string, *smtp.MailOptions) error {
	s.s.mu.Lock()
	s.s.mails++
	s.s.mu.Unlock()
	return nil
}

func (s *sess) Rcpt(to string, _ *smtp.RcptOptions) error {
	switch {
	case strings.HasPrefix(to, "bad"):
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "no such user"}
	case strings.HasPrefix(to, "later"):
		return &smtp.SMTPError{Code: 450, EnhancedCode: smtp.EnhancedCode{4, 2, 1}, Message: "try later"}
	case strings.HasPrefix(to, "slow"):
		return &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 1}, Message: "throttled"}
	}
	return nil
}

func (s *sess) Data(r io.Reader) error {
	b, _ := io.ReadAll(r)
	s.s.mu.Lock()
	s.s.datas++
	s.s.bodies = append(s.s.bodies, string(b))
	mode := s.s.dataMode
	s.s.mu.Unlock()
	switch mode {
	case "452":
		return &smtp.SMTPError{Code: 452, EnhancedCode: smtp.EnhancedCode{4, 3, 1}, Message: "disk full"}
	case "554":
		return &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "rejected"}
	case "close":
		_ = s.conn.Conn().Close()
	case "hang":
		<-s.s.release
	}
	return nil
}

func (s *sess) AuthMechanisms() []string { return s.s.authMechs }

func (s *sess) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, user, pass string) error {
		if user == "u" && pass == "p" {
			return nil
		}
		return &smtp.SMTPError{Code: 535, EnhancedCode: smtp.EnhancedCode{5, 7, 8}, Message: "bad credentials"}
	}), nil
}

type mode int

const (
	plain mode = iota
	implicit
	starttls
)

func start(t *testing.T, m mode, configure func(*srv, *smtp.Server)) (*srv, string) {
	t.Helper()
	sv := &srv{authMechs: []string{sasl.Plain}, release: make(chan struct{})}
	server := smtp.NewServer(smtp.BackendFunc(func(c *smtp.Conn) (smtp.Session, error) { return &sess{s: sv, conn: c}, nil }))
	server.Domain = "fake.test"
	server.AllowInsecureAuth = true
	server.EnableSMTPUTF8 = true
	if configure != nil {
		configure(sv, server)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	switch m {
	case implicit:
		ln = tls.NewListener(ln, selfSigned(t))
	case starttls:
		server.TLSConfig = selfSigned(t)
		server.AllowInsecureAuth = false
	}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() {
		select {
		case <-sv.release:
		default:
			close(sv.release)
		}
		_ = server.Close()
	})
	return sv, ln.Addr().String()
}

func selfSigned(t *testing.T) *tls.Config {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: k}}, MinVersion: tls.VersionTLS12}
}

func opts(addr string, m mode) SessionOptions {
	o := SessionOptions{Addr: addr, TLS: TLSNone, Username: "u", Password: "p", EHLO: "eb.test", CommandTimeout: 2 * time.Second, DataTimeout: 500 * time.Millisecond}
	switch m {
	case implicit:
		o.TLS, o.InsecureSkipVerify = TLSImplicit, true
	case starttls:
		o.TLS, o.InsecureSkipVerify = TLSStartTLS, true
	}
	return o
}

const body = "From: a@example.test\r\nTo: b@example.test\r\nSubject: hi\r\n\r\nhello\r\n.leading dot\r\n"

func env(to ...string) Envelope {
	return Envelope{From: "a@example.test", To: to, Size: int64(len(body))}
}

func stageOf(t *testing.T, err error) Stage {
	t.Helper()
	var se *StageError
	if !errors.As(err, &se) {
		t.Fatalf("no StageError in %v", err)
	}
	return se.Stage
}

func TestSendAllModes(t *testing.T) {
	for name, m := range map[string]mode{"none": plain, "implicit": implicit, "starttls": starttls} {
		t.Run(name, func(t *testing.T) {
			sv, addr := start(t, m, nil)
			var lines []string
			var mu sync.Mutex
			o := opts(addr, m)
			o.Tap = func(l protolog.Line) { mu.Lock(); lines = append(lines, string(l.Dir)+" "+l.Text); mu.Unlock() }
			ctx := context.Background()
			s, err := Open(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			var stages []Stage
			res, err := s.Send(ctx, env("ok@example.test", "bad@example.test"), strings.NewReader(body), Hooks{Stage: func(_ context.Context, st Stage) error {
				stages = append(stages, st)
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(stagesStr(stages), ",") != "mailFrom,rcptTo,data,dataEnd" {
				t.Fatalf("stages %v", stages)
			}
			if len(res.Accepted) != 1 || len(res.Rejected) != 1 || res.Rejected[0].Code != 550 || res.Rejected[0].Enhanced != "5.1.1" {
				t.Fatalf("result %+v", res)
			}
			// session reuse
			if err := s.Reset(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.Noop(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Send(ctx, env("ok2@example.test"), strings.NewReader(body), Hooks{}); err != nil {
				t.Fatal(err)
			}
			if s.Messages() != 2 || s.Dead() {
				t.Fatalf("messages %d dead %v", s.Messages(), s.Dead())
			}
			_ = s.Close()
			sv.mu.Lock()
			if sv.datas != 2 || !strings.Contains(sv.bodies[0], "\r\n.leading dot") {
				t.Fatalf("server got %d bodies: %q", sv.datas, sv.bodies)
			}
			sv.mu.Unlock()
			mu.Lock()
			defer mu.Unlock()
			all := strings.Join(lines, "\n")
			if !strings.Contains(all, "MAIL FROM") || strings.Contains(all, "hello") || strings.Contains(all, "dXAAcA") /* base64 "u\x00p" fragments */ {
				t.Fatalf("tap must be plaintext with the body and credentials masked:\n%s", all)
			}
			if m == starttls && !strings.Contains(all, "STARTTLS") {
				t.Fatalf("starttls transcript:\n%s", all)
			}
		})
	}
}

func stagesStr(st []Stage) []string {
	out := make([]string, len(st))
	for i, s := range st {
		out[i] = string(s)
	}
	return out
}

func TestRecipientFailures(t *testing.T) {
	_, addr := start(t, plain, nil)
	ctx := context.Background()
	s, err := Open(ctx, opts(addr, plain))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, err = s.Send(ctx, env("bad1@example.test", "bad2@example.test"), strings.NewReader(body), Hooks{})
	if errs.CodeOf(err) != errs.CodeSMTPPermanent || stageOf(t, err) != StageRcptTo {
		t.Fatalf("all rejected: %v", err)
	}
	_, err = s.Send(ctx, env("ok@example.test", "later@example.test"), strings.NewReader(body), Hooks{})
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatalf("temporary: %v", err)
	}
	_, err = s.Send(ctx, env("slow@example.test"), strings.NewReader(body), Hooks{})
	if errs.CodeOf(err) != errs.CodeSMTPRateLimited {
		t.Fatalf("throttle: %v", err)
	}
	if s.Dead() {
		t.Fatal("RCPT replies keep the session usable")
	}
	if _, err := s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{}); err != nil {
		t.Fatalf("after failures: %v", err)
	}
}

func TestStageHookStopsBeforeData(t *testing.T) {
	sv, addr := start(t, plain, nil)
	ctx := context.Background()
	s, err := Open(ctx, opts(addr, plain))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	boom := errors.New("majority write failed")
	_, err = s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{Stage: func(_ context.Context, st Stage) error {
		if st == StageData {
			return boom
		}
		return nil
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("hook error: %v", err)
	}
	sv.mu.Lock()
	datas := sv.datas
	sv.mu.Unlock()
	if datas != 0 || s.Dead() {
		t.Fatalf("DATA must not be sent (%d) and the session survives (dead=%v)", datas, s.Dead())
	}
	if _, err := s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{}); err != nil {
		t.Fatal(err)
	}
}

func TestAfterBodyOutcomes(t *testing.T) {
	cases := []struct {
		mode string
		code errs.Code
		st   Stage
	}{
		{"452", errs.CodeSMTPTemporary, StageDataEnd},
		{"554", errs.CodeSMTPPermanent, StageDataEnd},
		{"close", errs.CodeSMTPOutcomeUnknown, StageDataEnd},
		{"hang", errs.CodeSMTPOutcomeUnknown, StageDataEnd},
	}
	for _, c := range cases {
		t.Run(c.mode, func(t *testing.T) {
			sv, addr := start(t, plain, func(sv *srv, _ *smtp.Server) { sv.dataMode = "" })
			sv.mu.Lock()
			sv.dataMode = c.mode
			sv.mu.Unlock()
			ctx := context.Background()
			s, err := Open(ctx, opts(addr, plain))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			startAt := time.Now()
			_, err = s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{})
			if errs.CodeOf(err) != c.code || stageOf(t, err) != c.st {
				t.Fatalf("got %v (%s)", err, errs.CodeOf(err))
			}
			if time.Since(startAt) > 5*time.Second {
				t.Fatal("too slow: DataTimeout not applied")
			}
			if !s.Dead() {
				t.Fatal("a failure after the body kills the session")
			}
		})
	}
}

func TestCancelDuringData(t *testing.T) {
	sv, addr := start(t, plain, nil)
	sv.dataMode = "hang"
	o := opts(addr, plain)
	o.DataTimeout = time.Minute
	s, err := Open(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	startAt := time.Now()
	_, err = s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{})
	if errs.CodeOf(err) != errs.CodeSMTPOutcomeUnknown {
		t.Fatalf("cancel after body: %v", err)
	}
	if time.Since(startAt) > 3*time.Second {
		t.Fatal("cancellation must close the connection")
	}
}

func TestSizeAndUTF8(t *testing.T) {
	sv, addr := start(t, plain, func(_ *srv, s *smtp.Server) { s.MaxMessageBytes = 20; s.EnableSMTPUTF8 = false })
	ctx := context.Background()
	s, err := Open(ctx, opts(addr, plain))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if s.MaxSize() != 20 {
		t.Fatalf("max %d", s.MaxSize())
	}
	_, err = s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{})
	if errs.CodeOf(err) != errs.CodeMessageTooLarge {
		t.Fatalf("size: %v", err)
	}
	e := Envelope{From: "a@example.test", To: []string{"ok@example.test"}, UTF8: true}
	if _, err := s.Send(ctx, e, strings.NewReader("x"), Hooks{}); errs.CodeOf(err) != errs.CodeSMTPPermanent {
		t.Fatalf("utf8: %v", err)
	}
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.mails != 0 {
		t.Fatal("pre-checks must fail before MAIL FROM")
	}
	if _, err := s.Send(ctx, Envelope{From: "a@example.test"}, strings.NewReader("x"), Hooks{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("no recipients: %v", err)
	}
}

func TestOpenFailures(t *testing.T) {
	ctx := context.Background()
	_, addr := start(t, plain, nil)
	o := opts(addr, plain)
	o.Password = "wrong"
	if _, err := Open(ctx, o); errs.CodeOf(err) != errs.CodeSMTPAuthFailed || stageOf(t, err) != StageAuth {
		t.Fatalf("auth: %v", err)
	}
	_, addr2 := start(t, plain, func(sv *srv, _ *smtp.Server) { sv.authMechs = []string{"CRAM-MD5"} })
	if _, err := Open(ctx, opts(addr2, plain)); errs.CodeOf(err) != errs.CodeSMTPAuthFailed {
		t.Fatalf("no mechanism: %v", err)
	}
	if _, err := Open(ctx, SessionOptions{Addr: "127.0.0.1:1", TLS: TLSNone}); errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatalf("refused: %v", err)
	}
	if _, err := Open(ctx, SessionOptions{Addr: "nohostport"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("addr: %v", err)
	}
	if _, err := Open(ctx, SessionOptions{Addr: addr, TLS: "bogus"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("mode: %v", err)
	}
	// STARTTLS requested from a server that does not offer it
	if _, err := Open(ctx, SessionOptions{Addr: addr, TLS: TLSStartTLS}); errs.CodeOf(err) != errs.CodeMailTLSFailed {
		t.Fatalf("no starttls: %v", err)
	}
	// implicit TLS against plaintext, and an untrusted certificate
	if _, err := Open(ctx, SessionOptions{Addr: addr, TLS: TLSImplicit, ConnectTimeout: time.Second}); err == nil {
		t.Fatal("implicit TLS against plaintext must fail")
	}
	_, taddr := start(t, implicit, nil)
	if _, err := Open(ctx, SessionOptions{Addr: taddr, TLS: TLSImplicit}); err == nil {
		t.Fatal("self-signed certificate must be refused without InsecureSkipVerify")
	}
	// a dead session refuses work
	s, err := Open(ctx, opts(addr, plain))
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := s.Send(ctx, env("ok@example.test"), strings.NewReader(body), Hooks{}); err == nil || s.Reset(ctx) == nil || s.Noop(ctx) == nil {
		t.Fatal("closed session")
	}
}

func TestReadReply(t *testing.T) {
	for _, in := range []string{"2\r\n", "abc hello\r\n"} {
		if _, _, err := readReply(bufioReader(in)); err == nil {
			t.Fatalf("%q must fail", in)
		}
	}
	code, lines, err := readReply(bufioReader("250-a\r\n250-STARTTLS\r\n250 SIZE 10\r\n"))
	if err != nil || code != 250 || len(lines) != 3 || lines[1] != "STARTTLS" {
		t.Fatalf("%d %v %v", code, lines, err)
	}
	if (&StageError{Stage: StageConnect, Err: io.EOF}).Error() == "" || (&StageError{Stage: StageRcptTo, Code: 550}).Error() == "" {
		t.Fatal("Error()")
	}
}

func bufioReader(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }
