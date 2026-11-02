package smtpc

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		stage string
		err   error
		code  errs.Code
	}{
		{"421 mail", string(StageMailFrom), &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 3, 2}, Message: "busy"}, errs.CodeSMTPTemporary},
		{"450 rcpt", string(StageRcptTo), &smtp.SMTPError{Code: 450, EnhancedCode: smtp.EnhancedCode{4, 2, 1}}, errs.CodeSMTPTemporary},
		{"550 rcpt", string(StageRcptTo), &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}}, errs.CodeSMTPPermanent},
		{"451 4.7.1", string(StageRcptTo), &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 1}, Message: "throttle"}, errs.CodeSMTPRateLimited},
		{"535 auth", string(StageAuth), &smtp.SMTPError{Code: 535, EnhancedCode: smtp.EnhancedCode{5, 7, 8}}, errs.CodeSMTPAuthFailed},
		{"503", string(StageMailFrom), &smtp.SMTPError{Code: 503, EnhancedCode: smtp.EnhancedCode{5, 5, 1}}, errs.CodeSMTPTemporary},
		{"452 dataEnd", string(StageDataEnd), &smtp.SMTPError{Code: 452, EnhancedCode: smtp.EnhancedCode{4, 3, 1}}, errs.CodeSMTPTemporary},
		{"554 dataEnd", string(StageDataEnd), &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 7, 1}}, errs.CodeSMTPPermanent},
		{"eof dataEnd", string(StageDataEnd), io.EOF, errs.CodeSMTPOutcomeUnknown},
		{"timeout dataEnd", string(StageDataEnd), &timeoutErr{}, errs.CodeSMTPOutcomeUnknown},
		{"dial refused", string(StageConnect), errors.New("connection refused"), errs.CodeSMTPTemporary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(tc.stage, tc.err)
			if errs.CodeOf(got) != tc.code {
				t.Fatalf("code %s, want %s (%v)", errs.CodeOf(got), tc.code, got)
			}
		})
	}
	if Classify(string(StageMailFrom), nil) != nil {
		t.Fatal("nil err")
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestSendSimpleDeliveryAndRetry4xx(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var got []byte
	var mailFrom string
	var rcpts []string
	failOnce := true

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 2; i++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			serveScripted(conn, &mu, &got, &mailFrom, &rcpts, &failOnce)
		}
	}()

	host := "127.0.0.1"
	port := ln.Addr().(*net.TCPAddr).Port
	o := Options{Host: host, Port: port, TLS: TLSNone, EHLO: "eb-test"}
	raw := []byte("From: a@b\r\nTo: c@d\r\nSubject: hi\r\n\r\nbody\r\n")
	err = SendSimple(context.Background(), o, "a@b", []string{"c@d"}, raw)
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatalf("first send want temporary, got %v", err)
	}
	err = SendSimple(context.Background(), o, "a@b", []string{"c@d"}, raw)
	if err != nil {
		t.Fatalf("second send: %v", err)
	}
	<-done
	mu.Lock()
	defer mu.Unlock()
	if mailFrom != "a@b" || len(rcpts) != 1 || rcpts[0] != "c@d" {
		t.Fatalf("envelope from=%q rcpts=%v", mailFrom, rcpts)
	}
	if !strings.Contains(string(got), "Subject: hi") {
		t.Fatalf("body %q", got)
	}
}

func TestSendSimpleMissingHost(t *testing.T) {
	err := SendSimple(context.Background(), Options{}, "a@b", []string{"c@d"}, nil)
	if errs.CodeOf(err) != errs.CodeSMTPTemporary {
		t.Fatal(err)
	}
}

// serveScripted is a tiny SMTP sink: first connection fails RCPT with 450; second accepts DATA.
func serveScripted(conn net.Conn, mu *sync.Mutex, got *[]byte, mailFrom *string, rcpts *[]string, failOnce *bool) {
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(s string) {
		_, _ = w.WriteString(s)
		_ = w.Flush()
	}
	write("220 test ESMTP\r\n")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO") || strings.HasPrefix(upper, "HELO"):
			write("250-localhost\r\n250 AUTH PLAIN\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			mu.Lock()
			*mailFrom = extractAngle(line)
			mu.Unlock()
			write("250 OK\r\n")
		case strings.HasPrefix(upper, "RCPT TO:"):
			mu.Lock()
			fail := *failOnce
			if fail {
				*failOnce = false
			} else {
				*rcpts = append(*rcpts, extractAngle(line))
			}
			mu.Unlock()
			if fail {
				write("450 4.2.1 mailbox busy\r\n")
			} else {
				write("250 OK\r\n")
			}
		case upper == "DATA":
			write("354 Go\r\n")
			var buf strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
				buf.WriteString(l)
			}
			mu.Lock()
			*got = []byte(buf.String())
			mu.Unlock()
			write("250 OK\r\n")
		case upper == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("250 OK\r\n")
		}
	}
}

func extractAngle(line string) string {
	i := strings.IndexByte(line, '<')
	j := strings.IndexByte(line, '>')
	if i >= 0 && j > i {
		return line[i+1 : j]
	}
	parts := strings.SplitN(line, ":", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[1])
	}
	return ""
}
